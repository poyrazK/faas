//go:build unix

package preview

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestPostgresPoolCoordinatedACK(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var checks atomic.Int64
	var fail atomic.Bool
	b := newRuntimePool(t, func(context.Context, *runtimePool) error {
		if checks.Add(1) == 1 {
			close(entered)
			<-finish
		}
		return nil
	})
	f := newSecretFixture(t, func(options *faas.SecretReloadOptions, fixture *secretFixture) {
		fixture.raw = secretEnvelope("a", poolValues("private-first"))
		options.Consumers = []faas.SecretConsumer{b.SecretConsumer(), {
			Apply: func(context.Context, faas.SecretSnapshot) error {
				if fail.Load() {
					return errors.New("private-other-consumer")
				}
				return nil
			}, Invalidate: func() {},
		}}
	})
	done := make(chan error, 1)
	go func() { _, err := f.runtime.Start(context.Background()); done <- err }()
	poolWait(t, entered)
	_, acquisitionErr := b.Acquire()
	acks := f.acks()
	close(finish)
	if acquisitionErr == nil || len(acks) != 0 {
		t.Fatal("pool or ACK published before validation")
	}
	if err := poolWait(t, done); err != nil {
		t.Fatal(err)
	}
	old, _ := b.Acquire()
	pool, _ := old.Current()
	fail.Store(true)
	f.publish(secretEnvelope("b", poolValues("private-second")))
	if _, err := f.runtime.Reload(context.Background()); err == nil {
		t.Fatal("partial adoption succeeded")
	}
	acks = f.acks()
	if _, err := b.Acquire(); err == nil || pool.closed.Load() != 0 || len(acks) != 2 || acks[0]["status"] != "applied" || acks[1]["status"] != "failed" {
		t.Fatal("partial adoption violated transaction or ACK contract")
	}
	old.Release()
	f.runtime.Stop()
	drainPool(t, b)
}
