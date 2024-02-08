package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	sync "sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/google/uuid"
	"github.com/morfien101/dynamo-q/pkg/tables"
	log "github.com/sirupsen/logrus"
)

// Notes for the reader:
// AWS credentials are loaded from environment variables.
// Current process:
// Start listening for signals
// Start gRPC server
// Start trying to obtain the lock
//   Start heart beat process
//   Make notes of progress so we can stop the process and clean up resource we have created.
//   Have we created a queue entry?
//   Did we get the lock?
// On shutdown stop grpc, release lock, remove queue entry.
// gRPC grace will wait for 30 seconds before shutting down if there is a subscriber.

type status struct {
	mu                sync.RWMutex
	queueEntryCreated bool
	lockAcquired      bool
	grpcServerStarted bool
}

var (
	state = status{}
	ls    *LockServer
)

func helpMessage() {
	fmt.Println("Usage: lock_manager [flags]")
	fmt.Println("Flags:")
	fmt.Println("  -lock-table string")
	fmt.Println("        The name of the DynamoDB table to use.")
	fmt.Println("  -queue-table string")
	fmt.Println("        The name of the DynamoDB table to use.")
	fmt.Println("  -lock string")
	fmt.Println("        The name of the lock to acquire.")
	fmt.Println("  -client string")
	fmt.Println("        The unique identifier for this client. Default of nothing will generate a uuid for you.")
	fmt.Println("  -host string")
	fmt.Println("        The host to listen on. (default \"localhost\")")
	fmt.Println("  -port int")
	fmt.Println("        The port to listen on. (default 50051)")
	fmt.Println("  -log-level string")
	fmt.Println("        The log level to use. Options are: trace, debug, info, warn, error, fatal, panic (default \"info\")")
	fmt.Println("")
}

func main() {

	lockTableName := flag.String("lock-table", "", "The name of the DynamoDB table to use for Locks.")
	queueTableName := flag.String("queue-table", "", "The name of the DynamoDB table to use for Queues.")
	lockId := flag.String("lock", "", "The name of the lock to acquire.")
	clientName := flag.String("client", "", "The unique identifier for this client. Default of nothing will generate a uuid for you.")
	host := flag.String("host", "localhost", "The host to listen on.")
	port := flag.Int("port", 50051, "The port to listen on.")
	logLevel := flag.String("log-level", "info", "The log level to use. Options are: trace, debug, info, warn, error, fatal, panic")
	flag.Usage = helpMessage
	flag.Parse()

	// Set the log level
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		fmt.Println("Invalid log level")
		os.Exit(1)
	}
	log.SetLevel(level)

	if *clientName == "" {
		*clientName = uuid.NewString()
	}

	if *lockTableName == "" {
		fmt.Println("Lock table name is required.")
		os.Exit(1)
	}
	if *queueTableName == "" {
		fmt.Println("Queue table name is required.")
		os.Exit(1)
	}
	if *lockId == "" {
		fmt.Println("Lock ID is required.")
		os.Exit(1)
	}

	log.WithFields(log.Fields{"lockID": *lockId, "clientName": *clientName}).Info("Starting lock manager...")
	startTime := time.Now().Unix()
	grpcStopChan := make(chan bool)
	grpcErrChan := make(chan error)
	stopQueueHeartBeat := make(chan bool)
	stopLockHeartBeat := make(chan bool)
	shutdownRequest := make(chan bool)

	ls = newLockServer(shutdownRequest)
	ls.StartServer(*host, *port, grpcStopChan, grpcErrChan)

	// Initialize AWS session and DynamoDB client.
	awsSession := session.Must(session.NewSession())
	svc := dynamodb.New(awsSession)
	go obtainLock(svc, *lockId, *clientName, *queueTableName, *lockTableName, startTime, stopQueueHeartBeat, stopLockHeartBeat)

	// Set up channel to receive OS signals
	signals := make(chan os.Signal, 1)
	// Notify the channel on SIGINT (Ctrl+C) and SIGTERM (termination signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	exit := func() int {
		return attemptCleanExit(svc, *lockId, *lockTableName, *clientName, *queueTableName, startTime, grpcStopChan, stopLockHeartBeat, stopQueueHeartBeat, grpcErrChan)
	}
	// In both cases, attempt to clean up and exit
	select {
	case <-signals:
		os.Exit(exit())
	case <-shutdownRequest:
		os.Exit(exit())
	}
}

func attemptCleanExit(svc *dynamodb.DynamoDB, lockId, lockTableName, clientName, queueTableName string, startTime int64, grpcStopChan, stopLockHeartBeat, stopQueueHeartBeat chan bool, grpcErrChan chan error) int {
	exitCode := 0
	state.mu.RLock()
	if state.grpcServerStarted {
		log.Info("Received shutdown signal")
		// Stop the gRPC server
		grpcStopChan <- true
		// Wait for the gRPC server to stop
		err := <-grpcErrChan
		if err != nil {
			exitCode = 1
			log.Fatalf("gRPC server error: %v", err)
		}
	}
	state.mu.RUnlock()
	// Release the lock
	state.mu.RLock()
	if state.lockAcquired {
		stopLockHeartBeat <- true
		close(stopLockHeartBeat)
		err := releaseLock(svc, lockId, lockTableName)
		if err != nil {
			exitCode = 1
			log.Fatalf("Failed to release lock: %v", err)
		}
	}
	state.mu.RUnlock()
	// Remove the queue entry
	state.mu.RLock()
	stopQueueHeartBeat <- true
	close(stopQueueHeartBeat)
	if state.queueEntryCreated {
		err := releaseQueueSlot(svc, lockId, clientName, queueTableName, startTime)
		if err != nil {
			exitCode = 1
			log.Fatalf("Failed to release queue slot: %v", err)
		}
	}
	state.mu.RUnlock()
	return exitCode
}

func obtainLock(svc *dynamodb.DynamoDB, lockId, clientName, queueTableName, lockTableName string, startTime int64, stopQueueHeartBeat, stopLockHeartBeat chan bool) error {
	log.Info("Create queue entry")
	// Create a queue entry
	// Try 3 times to create a queue entry in case of network of throttling issues
	for try := 0; try <= 3; try++ {
		if try == 3 {
			return fmt.Errorf("failed to create queue entry after 3 attempts")
		}
		err := tables.CreateQueueEntry(svc, lockId, clientName, startTime, queueTableName, stopQueueHeartBeat)
		if err != nil {
			log.Errorf("try %d - failed to create queue entry:", err)
			time.Sleep(3 * time.Second)
			continue
		}
		state.mu.Lock()
		state.queueEntryCreated = true
		state.mu.Unlock()
		break
	}
	log.Info("Queue entry created successfully")

	if ok := <-tables.WaitForTurn(svc, lockId, clientName, queueTableName); !ok {
		log.Error("There was an error waiting for the lock. See logs for details")
		log.Info("Attempting to clear my queue entry")
		err := tables.DeleteQueueEntry(svc, lockId, clientName, startTime, queueTableName)
		if err != nil {
			log.Errorf("Failed to delete queue entry: %v\n", err)
		} else {
			log.Info("Queue entry deleted successfully.")
		}
		return fmt.Errorf("failed to acquire lock")
	} else {
		log.Info("At front of queue!")
	}

	log.Info("Acquiring lock...")
	// Attempt to acquire the lock
	err := tables.AcquireLock(svc, lockId, clientName, lockTableName, stopLockHeartBeat)
	if err != nil {
		log.Error("Failed to acquire lock:", err)
		return err
	}
	log.Info("Lock acquired successfully")
	state.mu.Lock()
	state.lockAcquired = true
	ls.AcquiredLock()
	state.mu.Unlock()

	return nil
}

func releaseLock(svc *dynamodb.DynamoDB, lockId, lockTableName string) error {
	log.Info("Releasing lock and cleaning up...")

	err := tables.ReleaseLock(svc, lockId, lockTableName)
	if err != nil {
		return fmt.Errorf("Failed to release lock: %v", err)
	} else {
		log.Info("Lock released successfully.")
	}

	return nil
}

func releaseQueueSlot(svc *dynamodb.DynamoDB, lockId, clientName, queueTableName string, startTime int64) error {
	log.Info("Releasing queue queue entry")
	err := tables.DeleteQueueEntry(svc, lockId, clientName, startTime, queueTableName)
	if err != nil {
		return fmt.Errorf("failed to delete queue entry: %v", err)
	} else {
		log.Info("Queue entry deleted successfully.")
	}

	return nil
}
