package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestGracefulShutdownWaitsForActiveStreamingHandler(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseStream := func() { releaseOnce.Do(func() { close(release) }) }
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = fmt.Fprint(w, "data: first\n\n")
			flusher.Flush()
		}
		<-release
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		releaseStream()
		<-serveDone
	}()

	response, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- server.Shutdown(context.Background())
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before stream drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseStream()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("graceful shutdown failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after stream release")
	}
}
