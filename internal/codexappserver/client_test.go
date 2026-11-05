package codexappserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// shellAppServer returns a client whose App Server is a synthetic shell script.
func shellAppServer(t *testing.T, script string) *Client {
	t.Helper()
	return startShellAppServer(t, script, nil)
}

func startShellAppServer(t *testing.T, script string, beforeWrite func()) *Client {
	t.Helper()
	client := New(Config{Command: []string{"sh", "-c", script, "sh"}, BaseEnv: []string{"PATH=" + os.Getenv("PATH")}})
	client.beforeWrite = beforeWrite
	if err := client.Start(); err != nil {
		t.Fatalf("start synthetic app-server: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func waitDone(t *testing.T, client *Client) {
	t.Helper()
	select {
	case <-client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("app-server connection was not released")
	}
}

func TestRequestCancelDuringBlockedWriteEndsConnection(t *testing.T) {
	writing := make(chan struct{})
	var once sync.Once
	client := startShellAppServer(t, "exec sleep 30", func() { once.Do(func() { close(writing) }) })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		// Larger than any pipe buffer, so the write blocks while the peer is not reading.
		result <- client.Request(ctx, "test/large", map[string]string{"data": strings.Repeat("x", 4<<20)}, nil)
	}()
	select {
	case <-writing:
	case <-time.After(10 * time.Second):
		t.Fatal("large request never reached the writer")
	}
	// The write is in progress and cannot finish: the peer never reads.
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return after cancellation")
	}
	waitDone(t, client)
	if err := client.Request(context.Background(), "test/after", nil, nil); err == nil {
		t.Fatal("request after a canceled write reused the connection")
	}
	select {
	case <-client.writerDone:
	default:
		t.Fatal("writer goroutine still running after release")
	}
}

func TestCanceledContextWritesNothingAndKeepsConnection(t *testing.T) {
	client := shellAppServer(t, "exec sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range writeQueueSize * 2 {
		if err := client.Notify(ctx, "test/notify", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("notification with a canceled context = %v", err)
		}
	}
	select {
	case <-client.Done():
		t.Fatal("canceled context before queueing ended the connection")
	default:
	}
	if len(client.writes) != 0 {
		t.Fatalf("canceled notifications were queued: %d", len(client.writes))
	}
}

func TestPendingRequestsFailWhenPeerExits(t *testing.T) {
	client := shellAppServer(t, "read line; exit 0")
	err := client.Request(context.Background(), "test/request", nil, nil)
	if err == nil {
		t.Fatal("request succeeded after peer exit")
	}
	waitDone(t, client)
	if err := client.Close(); err != nil {
		t.Fatalf("close after exit: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}

func TestServerRequestFloodEndsConnection(t *testing.T) {
	client := shellAppServer(t, `while :; do echo '{"jsonrpc":"2.0","id":1,"method":"test/approval"}'; done`)
	waitDone(t, client)
	if err := client.Err(); err == nil || !strings.Contains(err.Error(), "faster than they could be denied") {
		t.Fatalf("flood error = %v", err)
	}
}

func TestCloseKillsDescendantsInTheProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant")
	client := New(Config{
		Command: []string{"sh", "-c", `sleep 30 & printf '%s\n' "$!" > "$1"; exec sleep 30`, "sh", marker},
		BaseEnv: []string{"PATH=" + os.Getenv("PATH")},
	})
	var pid int
	t.Cleanup(func() {
		_ = client.Close()
		if pid > 1 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	if err := client.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(marker)
		if err == nil {
			parsed, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && parsed > 1 {
				pid = parsed
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant did not record its pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	started := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("Close after %s: %v", time.Since(started), err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close took %s", elapsed)
	}
	deadline = time.Now().Add(time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant %d still present: %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerRequestsAreDenied(t *testing.T) {
	log := filepath.Join(t.TempDir(), "denials")
	client := shellAppServer(t, `echo '{"jsonrpc":"2.0","id":7,"method":"test/approval"}'; read line; printf '%s\n' "$line" > `+log+`; read next`)
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		if strings.Contains(string(data), `"id":7`) && strings.Contains(string(data), "client does not permit server requests") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server request was not denied: %q", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitDone(t, client)
}
