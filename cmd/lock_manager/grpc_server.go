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

type LockServer struct {
	comms.UnimplementedLockServiceServer
	mu          sync.RWMutex
	subscribers map[string]updateSubscription
	hasLock     bool
	shutdown    chan bool
}

func newLockServer(shutdown chan bool) *LockServer {
	return &LockServer{
		hasLock:     false,
		shutdown:    shutdown,
		subscribers: map[string]updateSubscription{},
	}
}

func (s *LockServer) AcquiredLock() (*comms.LockResponse, error) {
	s.mu.Lock()
	s.hasLock = true
	s.mu.Unlock()

	s.updateSubscriptions()

	return &comms.LockResponse{}, nil
}

func (s *LockServer) updateSubscriptions() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, sub := range s.subscribers {
		sub.updates <- s.hasLock
	}
}

func (s *LockServer) IsLockHeld(ctx context.Context, req *comms.LockRequest) (*comms.LockResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return &comms.LockResponse{IsHeld: s.hasLock}, nil
}

func (s *LockServer) SubscribeLockStatus(req *comms.LockRequest, stream comms.LockService_SubscribeLockStatusServer) error {
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
			if err := stream.Send(&comms.LockResponse{IsHeld: update}); err != nil {
				// Handle error by removing the subscriber from the list.
				return
			}
		}
	}()

	// If the lock is currently held, send an update immediately.
	s.mu.RLock()
	if s.hasLock {
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

func (s *LockServer) Shutdown(ctx context.Context, req *comms.ShutdownRequest) (*comms.ShutdownResponse, error) {
	s.shutdown <- true
	return &comms.ShutdownResponse{}, nil
}

// Start gRPC server
func (s *LockServer) StartServer(host string, port int, stopChan chan bool, errChan chan error) {
	log.WithFields(log.Fields{"host": host, "port": strconv.Itoa(port)}).Info("Starting gRPC server")
	lis, err := net.Listen("tcp", host+":"+strconv.Itoa(port))
	if err != nil {
		errChan <- fmt.Errorf("gRPC failure: %v", err)
		// just swallow the stop signal
		go func(chan bool) { <-stopChan }(stopChan)
		return
	}

	grpcServer := grpc.NewServer()
	comms.RegisterLockServiceServer(grpcServer, s)
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
}
