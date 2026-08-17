package codexappserver

import (
	"encoding/json"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestRequestDoesNotHoldStateLockWhileWriting(t *testing.T) {
	writer := &blockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	client := New(Config{})
	client.cmd = &exec.Cmd{}
	client.enc = json.NewEncoder(writer)

	requestDone := make(chan error, 1)
	go func() {
		requestDone <- client.Request(t.Context(), "test/request", map[string]any{"value": "test"}, nil)
	}()
	<-writer.started

	lockAcquired := make(chan struct{})
	go func() {
		client.mu.Lock()
		client.mu.Unlock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		close(writer.release)
		<-requestDone
		t.Fatal("request held the client state lock while writing")
	}

	close(writer.release)
	if err := <-requestDone; err == nil {
		t.Fatal("synthetic write failure was ignored")
	}
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write([]byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return 0, errors.New("synthetic write failure")
}
