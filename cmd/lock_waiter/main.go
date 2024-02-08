package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	log "github.com/sirupsen/logrus"

	"github.com/morfien101/dynamo-q/pkg/comms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TODO
// One one time is lock available.
// Wait for lock, this is likely the most common use case.
// Send a shutdown signal to the lock manager.
// flags: host, port, log-level

func helpMessage() {
	fmt.Println("Usage: lock_waiter [flags]")
	fmt.Println("Flags:")
	fmt.Println("  -host string")
	fmt.Println("        The host to connect to gRPC on. (default \"localhost\")")
	fmt.Println("  -log-level string")
	fmt.Println("        The log level to use. Options are: trace, debug, info, warn, error, fatal, panic (default \"info\")")
	fmt.Println("  -port int")
	fmt.Println("        The port to connect on. (default 50051)")
	fmt.Println("  -shutdown")
	fmt.Println("        Action: Send a shutdown signal to the lock manager.")
	fmt.Println("  -wait-for-lock")
	fmt.Println("        Action: Wait for the lock to be available.")
	fmt.Println("  -try-once")
	fmt.Println("        Action: Only try to get the lock once.")
	fmt.Println("  -h")
	fmt.Println("        Show this help message.")
	fmt.Println("")
	fmt.Println("Acton flags: You can specify only one of the action flags per run.")
	fmt.Println("             They are executed in the order of try-once, wait-for-lock, shutdown.")

}

func main() {
	host := flag.String("host", "localhost", "The host to connect to gRPC on.")
	port := flag.Int("port", 50051, "The port to connect on.")
	logLevel := flag.String("log-level", "info", "The log level to use. Options are: trace, debug, info, warn, error, fatal, panic")
	waitForLock := flag.Bool("wait-for-lock", false, "Action: Wait for the lock to be available.")
	shutdown := flag.Bool("shutdown", false, "Action: Send a shutdown signal to the lock manager.")
	tryOnce := flag.Bool("try-once", false, "Action: Only try to get the lock once.")
	flag.Usage = helpMessage
	flag.Parse()

	// Set the log level
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %v", err)
	}
	log.SetLevel(level)

	if !*waitForLock && !*shutdown && !*tryOnce {
		log.Error("You must specify one of the following action flags: wait-for-lock, shutdown, try-once")
		os.Exit(1)
	}

	gRPCHost := *host + ":" + strconv.Itoa(*port)
	conn, err := grpc.Dial(gRPCHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.WithFields(log.Fields{"host": *host, "port": strconv.Itoa(*port)}).Fatalf("Could not connect to server: %v", err)
	}
	defer conn.Close()

	if *tryOnce {
		exitCode := 1
		if checkLockHeld(conn) {
			log.Info("Lock is held")
			exitCode = 0
		} else {
			log.Info("Lock is not held")
		}
		os.Exit(exitCode)
	}

	if *waitForLock {
		log.Info("Waiting for lock...")
		if ok, err := subscribeLockStatus(conn); ok {
			os.Exit(0)
		} else {
			log.Fatalf("Could not wait for lock: %v", err)
		}
	}

	if *shutdown {
		log.Info("Sending shutdown signal...")
		sendShutdown(conn)
	}
}

func checkLockHeld(conn *grpc.ClientConn) bool {
	client := comms.NewLockServiceClient(conn)
	resp, err := client.IsLockHeld(context.Background(), &comms.LockRequest{})
	if err != nil {
		log.Fatalf("Could not check lock status: %v", err)
	}
	return resp.IsHeld
}

func subscribeLockStatus(conn *grpc.ClientConn) (bool, error) {
	client := comms.NewLockServiceClient(conn)
	stream, err := client.SubscribeLockStatus(context.Background(), &comms.LockRequest{})
	if err != nil {
		return false, fmt.Errorf("could not subscribe to lock status: %v", err)
	}
	for {
		log.Debug("Waiting for response from stream...")
		status, err := stream.Recv()
		if err == io.EOF {
			// Stream closed
			break
		}
		if err != nil {
			return false, fmt.Errorf("error receiving from stream: %v", err)
		}
		if status.IsHeld {
			log.Info("Lock acquired")
			return true, nil
		}
	}
	// Stream closed
	return false, fmt.Errorf("stream closed before status received")
}

func sendShutdown(conn *grpc.ClientConn) {
	client := comms.NewLockServiceClient(conn)
	_, err := client.Shutdown(context.Background(), &comms.ShutdownRequest{})
	if err != nil {
		log.Fatalf("Could not send shutdown: %v", err)
	}
}
