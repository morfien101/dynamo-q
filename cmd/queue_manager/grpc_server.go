package main

import (
	context "context"
	"fmt"
	"net"
	"strconv"
	sync "sync"
	"time"

	"github.com/google/uuid"
	"github.com/morfien101/dynamo-q/pkg/comms"
	log "github.com/sirupsen/logrus"

	grpc "google.golang.org/grpc"
)

type updateSubscription struct {
	updates chan bool
}

func newSubscription() updateSubscription {
	return updateSubscription{
		updates: make(chan bool),
	}
}

type QueueServer struct {
	comms.UnimplementedQueueServiceServer
	mu          sync.RWMutex
	subscribers map[string]updateSubscription
	isFront     bool
	shutdown    chan bool
}

func newQueueServer(shutdown chan bool) *QueueServer {
	return &QueueServer{
		isFront:     false,
		shutdown:    shutdown,
		subscribers: map[string]updateSubscription{},
	}
}

func (s *QueueServer) AtFront() (*comms.QueueStatus, error) {
	s.mu.Lock()
	s.isFront = true
	s.mu.Unlock()

	s.updateSubscriptions()

	return &comms.QueueStatus{}, nil
}

func (s *QueueServer) updateSubscriptions() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sub := range s.subscribers {
		sub.updates <- s.isFront
	}
}

func (s *QueueServer) IsFrontOfQueue(ctx context.Context, req *comms.QueueRequest) (*comms.QueueStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return &comms.QueueStatus{IsFront: s.isFront}, nil
}

func (s *QueueServer) SubscribeQueueStatus(req *comms.QueueRequest, stream comms.QueueService_SubscribeQueueStatusServer) error {
	sub := newSubscription()
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("failed to generate UUID: %v", err)
	}
	s.subscribers[id.String()] = sub

	log.WithFields(log.Fields{"id": id.String()}).Info("New gRPC Subscriber")

	go func() {
		for update := range sub.updates {
			log.WithField("id", id.String()).Debug("Sending update to subscriber")
			if err := stream.Send(&comms.QueueStatus{IsFront: update}); err != nil {
				// Handle error by removing the subscriber from the list.
				return
			}
		}
	}()

	// If at the front of the queue, send an update immediately.
	s.mu.RLock()
	if s.isFront {
		sub.updates <- true
	}
	s.mu.RUnlock()

	// Block until the client disconnects or an error occurs.
	<-stream.Context().Done()

	// Client disconnected, clean up.
	delete(s.subscribers, id.String())
	log.WithFields(log.Fields{"id": id.String()}).Info("gRPC Subscriber disconnected")
	return nil
}

func (s *QueueServer) Shutdown(ctx context.Context, req *comms.ShutdownRequest) (*comms.ShutdownResponse, error) {
	s.shutdown <- true
	return &comms.ShutdownResponse{}, nil
}

// Start gRPC server
func (s *QueueServer) StartServer(host string, port int, stopChan chan bool, errChan chan error) error {
	log.WithFields(log.Fields{"host": host, "port": strconv.Itoa(port)}).Info("Starting gRPC server")
	lis, err := net.Listen("tcp", host+":"+strconv.Itoa(port))
	if err != nil {
		go func(chan bool) { <-stopChan }(stopChan)
		// just swallow the stop signal
		return fmt.Errorf("gRPC failure: %v", err)
	}

	grpcServer := grpc.NewServer()
	comms.RegisterQueueServiceServer(grpcServer, s)
	go func(chan error) {
		state.mu.Lock()
		state.grpcServerStarted = true
		state.mu.Unlock()
		if err := grpcServer.Serve(lis); err != nil {
			state.mu.Lock()
			state.grpcServerStarted = false
			state.mu.Unlock()
			log.Fatalf("failed to start GRPC server: %v", err)
			errChan <- fmt.Errorf("failed to start GRPC server: %v", err)
		}
	}(errChan)

	go func(chan bool, chan error) {
		<-stopChan

		ticker := time.NewTicker(30 * time.Second)
		serverStopped := make(chan bool)
		go func() {
			grpcServer.GracefulStop()
			serverStopped <- true
			ticker.Stop()
		}()

		select {
		case <-serverStopped:
		case <-ticker.C:
			log.Error("gRPC server did not stop in time, forcing shutdown!")
			grpcServer.Stop()
		}

		errChan <- nil
	}(stopChan, errChan)

	return nil
}
