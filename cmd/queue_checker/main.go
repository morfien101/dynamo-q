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

var (
	version = "development"
)

func helpMessage() {
	fmt.Println("Acton flags: You can specify only one of the action flags per run.")
	fmt.Println("             They are executed in the order of try-once, wait-for-turn, shutdown.")
}

func printVersion() {
	fmt.Println(version)
}

func main() {
	host := flag.String("host", "localhost", "The host to connect to gRPC on.")
	port := flag.Int("port", 50051, "The port to connect on.")
	logLevel := flag.String("log-level", "info", "The log level to use. Options are: trace, debug, info, warn, error, fatal, panic")
	waitForQueue := flag.Bool("wait-for-turn", false, "Action: Wait till front of queue.")
	shutdown := flag.Bool("shutdown", false, "Action: Send a shutdown signal to the queue manager.")
	tryOnce := flag.Bool("try-once", false, "Action: Only try once to see if you are at the front of the queue.")
	showVersion := flag.Bool("v", false, "Shows the version.")
	help := flag.Bool("h", false, "Shows the help message.")
	flag.Parse()

	if *help {
		flag.PrintDefaults()
		helpMessage()
		os.Exit(0)
	}

	// Set the log level
	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		log.Fatalf("Invalid log level: %v", err)
	}
	log.SetLevel(level)

	if *showVersion {
		printVersion()
		os.Exit(0)
	}

	if !*waitForQueue && !*shutdown && !*tryOnce {
		log.Error("You must specify one of the following action flags: wait-for-turn, shutdown, try-once")
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
		if frontOfQueue(conn) {
			log.Info("At the front of the queue!")
			exitCode = 0
		} else {
			log.Info("Waiting in line...")
		}
		os.Exit(exitCode)
	}

	if *waitForQueue {
		log.Info("Waiting in line...")
		if ok, err := subscribeQueueStatus(conn); ok {
			os.Exit(0)
		} else {
			log.Fatalf("Could not join check queue status: %v", err)
		}
	}

	if *shutdown {
		log.Info("Sending shutdown signal...")
		sendShutdown(conn)
	}
}

func frontOfQueue(conn *grpc.ClientConn) bool {
	client := comms.NewQueueServiceClient(conn)
	resp, err := client.IsFrontOfQueue(context.Background(), &comms.QueueRequest{})
	if err != nil {
		log.Fatalf("Could not check queue status: %v", err)
	}
	return resp.IsFront
}

func subscribeQueueStatus(conn *grpc.ClientConn) (bool, error) {
	client := comms.NewQueueServiceClient(conn)
	stream, err := client.SubscribeQueueStatus(context.Background(), &comms.QueueRequest{})
	if err != nil {
		return false, fmt.Errorf("could not subscribe to queue status: %v", err)
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
		if status.IsFront {
			log.Info("At front of queue!")
			return true, nil
		}
	}
	// Stream closed
	return false, fmt.Errorf("stream closed before status received")
}

func sendShutdown(conn *grpc.ClientConn) {
	client := comms.NewQueueServiceClient(conn)
	_, err := client.Shutdown(context.Background(), &comms.ShutdownRequest{})
	if err != nil {
		log.Fatalf("Could not send shutdown: %v", err)
	}
}
