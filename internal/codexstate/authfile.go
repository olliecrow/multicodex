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
	return readStateFile(path, "auth.json", MaxAuthFileBytes, true)
}

// MaxConfigFileBytes limits configuration and account metadata to 16 MiB.
const MaxConfigFileBytes = 16 << 20

// ReadMetadataFile applies auth-file descriptor checks to private metadata.
func ReadMetadataFile(path string) ([]byte, error) {
	data, _, err := readStateFile(path, "metadata file", MaxConfigFileBytes, true)
	return data, err
}

// ReadConfigFile permits shared config symlinks, but rejects special files.
func ReadConfigFile(path string) ([]byte, error) {
	data, _, err := readStateFile(path, "config file", MaxConfigFileBytes, false)
	return data, err
}

func readStateFile(path, label string, limit int64, private bool) ([]byte, os.FileInfo, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if private {
		flags |= syscall.O_NOFOLLOW
	}
	file, err := os.OpenFile(path, flags, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		if errors.Is(err, syscall.ELOOP) {
			return nil, nil, errors.New(label + " is a symlink")
		}
		return nil, nil, errors.New(label + " could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.New(label + " could not be inspected")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New(label + " is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, nil, errors.New(label + " ownership could not be checked")
	}
	if private && stat.Nlink > 1 {
		return nil, nil, errors.New(label + " has multiple hard links")
	}
	if private && int(stat.Uid) != os.Getuid() {
		return nil, nil, errors.New(label + " is not owned by the current user")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, nil, errors.New(label + " could not be read")
	}
	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("%s exceeds %d MiB", label, limit>>20)
	}
	return data, info, nil
}
