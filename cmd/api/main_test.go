package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeWaitsForInFlightRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		close(finished)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, h) }()
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("serve returned before the in-flight request finished")
	}
}
