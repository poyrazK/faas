package e2etest

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestNativeRestartNeverTargetsAProcessByArgumentName(t *testing.T) {
	child := exec.Command("sleep", "30")
	child.Args[0] = "vmmd"
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	h := &Harness{T: t, BinDir: t.TempDir(), SockDir: t.TempDir(), GatewayControlURL: "http://127.0.0.1:1", procs: []*exec.Cmd{child}}
	h.VMMDSock = h.SockDir + "/vmmd.sock"
	if h.RestartVMMD() == nil {
		t.Fatal("accepted a foreign executable named vmmd in argv")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("restart killed a foreign process", err)
	}
	if h.RestartGateway() == nil {
		t.Fatal("accepted an unowned gateway")
	}
	child.Path = h.BinDir + "/vmmd"
	h.VMMDSock = "/foreign/vmmd.sock"
	if h.RestartVMMD() == nil {
		t.Fatal("accepted a socket outside the harness")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("socket validation happened after killing the child", err)
	}
}

func TestNativeRestartAllowsOnlyExplicitlyStoppedPeers(t *testing.T) {
	children := []*exec.Cmd{exec.Command("true"), exec.Command("true")}
	for _, child := range children {
		if err := child.Run(); err != nil {
			t.Fatal(err)
		}
	}
	h := &Harness{T: t, procs: children}
	h.markDaemonStopped(children[0])
	if watched := h.watchedDaemons(); len(watched) != 1 || watched[0] != children[1] {
		t.Fatal("controlled stop masked another daemon")
	}
	if _, _, dead := firstDeadDaemon(h.watchedDaemons()); !dead {
		t.Fatal("unexpected peer exit was hidden")
	}
	h.procs = children[:1]
	h.requireDaemonsAlive(t)
}
