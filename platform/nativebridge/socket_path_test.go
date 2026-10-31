package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareSocketPreservesFilesAndLiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "asrobot-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "socket")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocket(path); err == nil {
		t.Fatal("ordinary file removed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("ordinary file missing")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareSocket(path); err == nil {
		t.Fatal("live socket removed")
	}
	listener.Close()
	// Go removes the socket on Close by default; create a stale one explicitly.
	listener, err = net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
	if err := prepareSocket(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("stale socket remains")
	}
}
