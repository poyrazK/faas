package e2etest

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFinancialVMMDChild(t *testing.T) {
	if os.Getenv("FAAS_FINANCIAL_PROCESS_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("unix", os.Getenv("FAAS_FINANCIAL_PROCESS_SOCKET"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	identity := fmt.Sprintf("%s:%s", os.Getenv("FAAS_FINANCIAL_RECIPE"), uuid.NewString())
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_, _ = io.WriteString(conn, identity)
		_ = conn.Close()
	}
}

func TestFinancialVMMDProcessRestart(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	socketDir, err := os.MkdirTemp("", "cost-vmmd-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "v.sock")
	if err := os.WriteFile(filepath.Join(dir, "vmmd"), []byte("#!/bin/sh\nexec \"$FAAS_FINANCIAL_HELPER_BIN\" -test.run=^TestFinancialVMMDChild$\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{"FAAS_FINANCIAL_PROCESS_HELPER=1", "FAAS_FINANCIAL_HELPER_BIN=" + bin, "FAAS_FINANCIAL_PROCESS_SOCKET=" + socket, "FAAS_FINANCIAL_RECIPE=retained"}
	h := &Harness{T: t, BinDir: dir, VMMDSock: socket, vmmdLaunchEnv: env}
	h.procs = append(h.procs, startProc(t, dir, "vmmd", env))
	t.Cleanup(h.Stop)
	waitUnix(t, socket, 5e9)
	identity := func() string {
		t.Helper()
		conn, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		data, err := io.ReadAll(conn)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	before := identity()
	original := h.procs[0]
	if err := h.RestartVMMD(); err != nil {
		t.Fatal(err)
	}
	after := identity()
	if original.ProcessState == nil || len(h.procs) != 1 || before == after || !strings.HasPrefix(after, "retained:") {
		t.Fatalf("restart did not reap and preserve recipe: before=%q after=%q procs=%d", before, after, len(h.procs))
	}
	if err := h.KillVMMD(); err != nil {
		t.Fatal(err)
	}
	if err := h.KillVMMD(); err != nil {
		t.Fatal(err)
	}
}
