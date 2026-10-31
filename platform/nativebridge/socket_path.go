package main

import (
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

// prepareSocket removes only an owned, inactive Unix socket. Never replace an
// ordinary file, a symlink, or a socket belonging to a live process.
func prepareSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != uint32(os.Getuid()) {
		return errors.New("socket path is not an owned Unix socket")
	}
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		return errors.New("socket is already active")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, os.ErrNotExist) {
		return errors.New("socket state is uncertain")
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return errors.New("socket changed during recovery")
	}
	return os.Remove(path)
}
