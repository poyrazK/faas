package credentialkeys

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/credhash"
)

type fakeRekeyer struct {
	results []int
	err     error
	calls   int
	done    chan struct{}
}

func (f *fakeRekeyer) RekeyCredentials(context.Context, int) (int, error) {
	f.calls++
	if f.calls > len(f.results) {
		close(f.done)
		return 0, f.err
	}
	return f.results[f.calls-1], nil
}

func installKeyed(t *testing.T, keyed bool) {
	t.Helper()
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	credhash.SetAccount(&credhash.Policy{Ring: ring, KeyedWrites: keyed, PlainLookup: true})
	t.Cleanup(func() { credhash.SetAccount(nil) })
}

// adr: 20261029202403 decision 4.2 — the backfill runs only with keyed
// writes on, and runs passes back to back until one rewrites nothing.
func TestRunRekey(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	installKeyed(t, false)
	idle := &fakeRekeyer{done: make(chan struct{})}
	RunRekey(context.Background(), idle, log)
	if idle.calls != 0 {
		t.Fatalf("rekeyed %d times with keyed writes off", idle.calls)
	}
	RunRekey(context.Background(), struct{}{}, log) // a store that cannot rekey

	for _, tc := range []struct {
		name string
		err  error
	}{{name: "until a pass rewrites nothing"}, {name: "until a pass fails", err: errors.New("db down")}} {
		t.Run(tc.name, func(t *testing.T) {
			installKeyed(t, true)
			r := &fakeRekeyer{results: []int{500, 500, 12}, err: tc.err, done: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			stopped := make(chan struct{})
			go func() { RunRekey(ctx, r, log); close(stopped) }()
			select {
			case <-r.done:
			case <-time.After(5 * time.Second):
				t.Fatal("backfill did not run its passes")
			}
			cancel()
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("backfill did not stop")
			}
			if r.calls != 4 {
				t.Fatalf("ran %d passes, want 4", r.calls)
			}
		})
	}
}
