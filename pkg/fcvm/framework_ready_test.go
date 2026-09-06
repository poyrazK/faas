package fcvm

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkready"
)

type readyStamperFunc func(context.Context, string, time.Time) error

func (f readyStamperFunc) SetFrameworkReadyAt(ctx context.Context, id string, ts time.Time) error {
	return f(ctx, id, ts)
}
func TestFrameworkReadyBridgeRoundTripAndCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fr-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('\n')
		if line != "CONNECT 1027\n" {
			return
		}
		_, _ = c.Write([]byte("OK 100\n"))
		_ = frameworkready.Write(c, frameworkready.Status{Ready: true, WarmupMs: 123})
	}()
	got, err := ReadFrameworkReady(context.Background(), socket)
	<-done
	if err != nil || got != (frameworkready.Status{Ready: true, WarmupMs: 123}) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ReadFrameworkReady(ctx, socket); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestFrameworkReadyLoopSurvivesWakeAndStopsOnDestroy(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	entered := make(chan struct{})
	canceled := make(chan struct{})
	m.WithFrameworkReadyReader(func(ctx context.Context, _ string) (frameworkready.Status, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return frameworkready.Status{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	r := req("ready-cancel")
	r.Runtime = "node22"
	if _, err := m.ColdBoot(ctx, r); err != nil {
		t.Fatal(err)
	}
	<-entered
	cancel()
	select {
	case <-canceled:
		t.Fatal("wake cancellation stopped observer")
	case <-time.After(20 * time.Millisecond):
	}
	if err := m.Destroy(context.Background(), r.Instance); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("destroy returned before observer stopped")
	}
	m.mu.Lock()
	n := len(m.frameworkReadyRuns)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("leaked observers %d", n)
	}
}
func TestFrameworkReadyLoopRetriesPersistence(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	var calls atomic.Int32
	done := make(chan struct{})
	m.WithFrameworkReadyStamper(readyStamperFunc(func(context.Context, string, time.Time) error {
		if calls.Add(1) == 1 {
			return errors.New("temporary persistence failure")
		}
		close(done)
		return nil
	}))
	m.WithFrameworkReadyReader(func(context.Context, string) (frameworkready.Status, error) {
		return frameworkready.Status{Ready: true, WarmupMs: 123}, nil
	})
	r := req("ready-retry")
	r.Runtime = "node22"
	if _, err := m.ColdBoot(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Destroy(context.Background(), r.Instance) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("persistence was not retried")
	}
	m.cancelFrameworkReadyLoop(r.Instance)
	if calls.Load() != 2 {
		t.Fatalf("persistence calls=%d", calls.Load())
	}
}
