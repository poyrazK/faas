package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerTaskIsolationRejectsStartedWorkerAndListener(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := assertWorkerTaskIsolation(marker, address); err == nil {
		listener.Close()
		t.Fatal("accepted a worker listener")
	}
	listener.Close()
	if err := assertWorkerTaskIsolation(marker, address); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("worker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := assertWorkerTaskIsolation(marker, address); err == nil {
		t.Fatal("accepted a captured worker startup marker")
	}
	if err := assertWorkerTaskIsolation(filepath.Join(marker, "unreadable"), address); err == nil {
		t.Fatal("accepted an uninspectable marker")
	}
}
