package multicodex

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestHeartbeatLockOpenNonblocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	f, ok, err := openHeartbeatLock(path)
	if err != nil || !ok {
		t.Fatalf("open: %v", err)
	}
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	f.Close()
	if err != nil || flags&unix.O_NONBLOCK == 0 {
		t.Fatalf("blocking lock open: flags %d, %v", flags, err)
	}
	// Replace the observed regular file before the descriptor is opened.
	if _, err := os.Lstat(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if f, _, err := openHeartbeatLock(path); err == nil {
		f.Close()
		t.Fatal("FIFO lock accepted")
	}
}
