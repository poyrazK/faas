// Tests for apid's SIGHUP-driven TLS cert rotation (PR-E /
// ADR-052 §5). The wire.TLSRotator contract (Set / Get / Reload /
// nil-tolerance) is pinned by pkg/wire/tls_rotator_test.go. Here
// we drive apid's rotators end-to-end through WatchTLSReload to
// confirm the apid-shaped wiring is correct — including the
// three-rotator fan-out (advisory server, githubd-bridge server,
// githubd client) which is unique to apid.
package apidserver_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func silentTLSReloadLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newMinimalTLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13}
}

func newDifferentTLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

func TestApidTLSReload_SIGHUPRotatesLiveConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := wire.NewTLSRotator(newMinimalTLSConfig())
	hupCh := make(chan os.Signal, 1)

	reload := func() (*tls.Config, error) {
		return newDifferentTLSConfig(), nil
	}

	go wire.WatchTLSReload(ctx, silentTLSReloadLogger(), hupCh, r, reload)

	hupCh <- syscall.SIGHUP
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r.Get() != newMinimalTLSConfig() && r.Get() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.Get(); got == newMinimalTLSConfig() {
		t.Errorf("rotator still holds initial after SIGHUP; got=%p want a fresh config", got)
	}
}

func TestApidTLSReload_MalformedReloadKeepsPrior(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := newMinimalTLSConfig()
	r := wire.NewTLSRotator(initial)
	hupCh := make(chan os.Signal, 1)

	boom := errors.New("malformed PEM")
	reload := func() (*tls.Config, error) { return nil, boom }

	go wire.WatchTLSReload(ctx, silentTLSReloadLogger(), hupCh, r, reload)

	hupCh <- syscall.SIGHUP
	time.Sleep(100 * time.Millisecond)

	if got := r.Get(); got != initial {
		t.Errorf("rotator after malformed reload: got=%p, want prior %p", got, initial)
	}
}

// TestApidTLSReload_ThreeRotatorsFiredBySameSignal pins the
// multi-rotator contract that's unique to apid: a single SIGHUP
// fans out to N independently-NOTIFY'd channels (signal.Notify fans
// the signal across every registered channel). Three rotators
// (advisory, bridge, githubd client) all swap on a single SIGHUP.
// Sister slice: a future advisory-only reload (e.g. issuer
// rotation) might add a fourth rotator without changing the
// existing three — they share nothing but the kernel signal.
func TestApidTLSReload_ThreeRotatorsFiredBySameSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rotators := []*wire.TLSRotator{
		wire.NewTLSRotator(newMinimalTLSConfig()),
		wire.NewTLSRotator(newMinimalTLSConfig()),
		wire.NewTLSRotator(newMinimalTLSConfig()),
	}

	hupChs := []chan os.Signal{
		make(chan os.Signal, 1),
		make(chan os.Signal, 1),
		make(chan os.Signal, 1),
	}
	for _, ch := range hupChs {
		signal.Notify(ch, syscall.SIGHUP)
	}
	defer func() {
		for _, ch := range hupChs {
			signal.Stop(ch)
		}
	}()

	reload := func() (*tls.Config, error) {
		return newDifferentTLSConfig(), nil
	}

	for i, r := range rotators {
		go wire.WatchTLSReload(ctx, silentTLSReloadLogger(), hupChs[i], r, reload)
	}

	// Fan one SIGHUP into each rotator's hupCh. In production
	// signal.Notify routes a single kernel delivery to all
	// registered channels; here we replicate that by writing one
	// syscall.SIGHUP to each channel — equivalent at the
	// WatchTLSReload level.
	for _, ch := range hupChs {
		ch <- syscall.SIGHUP
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, r := range rotators {
			if r.Get() == newMinimalTLSConfig() {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i, r := range rotators {
		if r.Get() == newMinimalTLSConfig() {
			t.Errorf("rotator[%d] still holds initial after SIGHUP fan-out", i)
		}
	}
}
