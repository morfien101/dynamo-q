package main

import (
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestWaitForServer(t *testing.T) {
	// Grab a free port, then release it so nothing is listening yet.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Nothing listening: should time out.
	if err := waitForServer(conn, 500*time.Millisecond); err == nil {
		t.Fatal("expected timeout with no server listening")
	}

	// Server comes up late: should connect.
	go func() {
		time.Sleep(time.Second)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		s := grpc.NewServer()
		t.Cleanup(s.Stop)
		s.Serve(l)
	}()
	if err := waitForServer(conn, 10*time.Second); err != nil {
		t.Fatalf("expected connection once server started: %v", err)
	}
}
