package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	sync "sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/morfien101/dynamo-q/pkg/queue"
	log "github.com/sirupsen/logrus"
)

// Notes for the reader:
// Credentials are loaded from environment variables (AWS or GCP depending on backend).
// Current process:
// Start listening for signals
// Start gRPC server
// Start trying work your way through the queue
//   Start heart beat process
//   Make notes of progress so we can stop the process and clean up resource we have created.
//   Have we created a queue entry?
//   Did we get to the front?
// On shutdown remove queue entry then stop grpc.
// gRPC grace will wait for 30 seconds before shutting down if there is a subscriber.

type status struct {
	mu                sync.RWMutex
	queueEntryCreated bool
	AtFront           bool
	grpcServerStarted bool
}

var (
	state   = status{}
	qs      *QueueServer
	version = "development"
)

func printVersion() {
	fmt.Println(version)
}

func main() {

	queueTableName := flag.String("queue-table", "", "The queue storage name (DynamoDB table or Firestore collection).")
	queueName := flag.String("queue-name", "", "The name of the queue to join.")
	clientName := flag.String("client", "", "The unique identifier for this client. Default of nothing will generate a uuid for you.")
	host := flag.String("host", "localhost", "The host to listen on.")
	port := flag.Int("port", 50051, "The port to listen on.")
	backend := flag.String("backend", "", "The queue backend to use. Options: aws, gcp")
	gcpProject := flag.String("gcp-project", "", "The GCP project ID to use when backend is gcp.")
	logLevel := flag.String("log-level", "info", "The log level to use. Options are: trace, debug, info, warn, error, fatal, panic")
	showVersion := flag.Bool("v", false, "Shows the version.")
	help := flag.Bool("h", false, "Shows the help message.")
	flag.Parse()

	if *help {
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Set the log level
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		fmt.Println("Invalid log level")
		os.Exit(1)
	}
	log.SetLevel(level)

	if *showVersion {
		printVersion()
		os.Exit(0)
	}

	if *clientName == "" {
		*clientName = uuid.NewString()
	}

	if *queueTableName == "" {
		fmt.Println("Queue table name is required.")
		os.Exit(1)
	}
	if *queueName == "" {
		fmt.Println("Queue Name is required.")
		os.Exit(1)
	}

	ctx := context.Background()
	log.WithFields(log.Fields{"queueName": *queueName, "clientName": *clientName}).Info("Starting queue manager...")
	startTime := time.Now().Unix()
	grpcStopChan := make(chan bool)
	grpcErrChan := make(chan error)
	stopQueueHeartBeat := make(chan bool)
	shutdownRequest := make(chan bool)

	qs = newQueueServer(shutdownRequest)
	err = qs.StartServer(*host, *port, grpcStopChan, grpcErrChan)
	if err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}

	store, err := queue.NewStore(ctx, *backend, *queueTableName, *gcpProject)
	if err != nil {
		log.Fatalf("Failed to initialize queue backend: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.WithFields(log.Fields{"error": err}).Warn("Failed to close queue backend")
		}
	}()

	err = createQueueEntry(ctx, store, *queueName, *clientName, startTime, stopQueueHeartBeat)
	if err != nil {
		log.Error("Failed to create queue entry: ", err)
		os.Exit(1)
	}

	go waitForTurn(ctx, store, *queueName, *clientName, startTime)

	// Set up channel to receive OS signals
	signals := make(chan os.Signal, 1)
	// Notify the channel on SIGINT (Ctrl+C) and SIGTERM (termination signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	exit := func() int {
		return attemptCleanExit(ctx, store, *queueName, *clientName, startTime, grpcStopChan, stopQueueHeartBeat, grpcErrChan)
	}
	// In both cases, attempt to clean up and exit
	select {
	case <-signals:
		os.Exit(exit())
	case <-shutdownRequest:
		os.Exit(exit())
	}
}

func attemptCleanExit(ctx context.Context, store queue.Store, queueName, clientName string, startTime int64, grpcStopChan, stopQueueHeartBeat chan bool, grpcErrChan chan error) int {
	exitCode := 0
	state.mu.RLock()
	defer state.mu.RUnlock()

	// Remove the queue entry
	stopQueueHeartBeat <- true
	close(stopQueueHeartBeat)
	if state.queueEntryCreated {
		err := releaseQueueSlot(ctx, store, queueName, clientName, startTime)
		if err != nil {
			exitCode = 1
			log.Fatalf("Failed to release queue slot: %v", err)
		}
	}

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

	return exitCode
}

func createQueueEntry(ctx context.Context, store queue.Store, queueName, clientName string, startTime int64, stopQueueHeartBeat chan bool) error {
	log.Info("Create queue entry")
	// Create a queue entry
	// Try 3 times to create a queue entry in case of network of throttling issues
	for try := 0; try <= 3; try++ {
		if try == 3 {
			return fmt.Errorf("failed to create queue entry after 3 attempts")
		}
		err := store.CreateEntry(ctx, queueName, clientName, startTime)
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
	queue.StartHeartbeat(ctx, store, queueName, startTime, stopQueueHeartBeat)

	return nil
}

func waitForTurn(ctx context.Context, store queue.Store, queueName, clientName string, startTime int64) error {
	if ok := <-queue.WaitForTurn(ctx, store, queueName, clientName); !ok {
		log.Error("There was an error waiting in the queue. See logs for details")
		log.Info("Attempting to clear my queue entry")
		err := store.DeleteEntry(ctx, queueName, startTime)
		if err != nil {
			log.Errorf("Failed to delete queue entry: %v", err)
		} else {
			log.Info("Queue entry deleted successfully.")
		}
		return fmt.Errorf("failed to acquire to get to the front of queue")
	} else {
		log.Info("At front of queue!")
	}

	state.mu.Lock()
	state.AtFront = true
	qs.AtFront()
	state.mu.Unlock()

	return nil
}

func releaseQueueSlot(ctx context.Context, store queue.Store, queueName, clientName string, startTime int64) error {
	log.Info("Releasing queue queue entry")
	err := store.DeleteEntry(ctx, queueName, startTime)
	if err != nil {
		return fmt.Errorf("failed to delete queue entry: %v", err)
	} else {
		log.Info("Queue entry deleted successfully.")
	}

	return nil
}
