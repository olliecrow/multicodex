package codexstate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// MaxAuthFileBytes bounds how much of a Codex auth file is read.
const MaxAuthFileBytes = 1 << 20

// ReadAuthFile reads a Codex auth file without following a final symlink or
// blocking on special files. It validates the opened descriptor, not the
// path, as a regular, single-link file owned by the current user, and reads
// at most MaxAuthFileBytes. Callers decide how to treat permissive modes.
func ReadAuthFile(path string) ([]byte, os.FileInfo, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		if errors.Is(err, syscall.ELOOP) {
			return nil, nil, errors.New("auth.json is a symlink")
		}
		return nil, nil, errors.New("auth.json could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.New("auth.json could not be inspected")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("auth.json is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, nil, errors.New("auth.json ownership could not be checked")
	}
	if stat.Nlink > 1 {
		return nil, nil, errors.New("auth.json has multiple hard links")
	}
	if int(stat.Uid) != os.Getuid() {
		return nil, nil, errors.New("auth.json is not owned by the current user")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxAuthFileBytes+1))
	if err != nil {
		return nil, nil, errors.New("auth.json could not be read")
	}
	if len(data) > MaxAuthFileBytes {
		return nil, nil, fmt.Errorf("auth.json exceeds %d MiB", MaxAuthFileBytes>>20)
	}
	return data, info, nil
}
