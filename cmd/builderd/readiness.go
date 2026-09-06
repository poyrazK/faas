// Package main — readiness.go constructs the builderd-side
// /readyz probe (issue #571 PR-A2). Three signals:
//
//   - PG ping: builderd reads from `builds` and writes the
//     `build_complete` / `build_failed` rows. A degraded
//     pgxpool stalls every queued build; /readyz flips to 503
//     so the LB stops routing build traffic.
//   - vmmd RPC dialable: every build spawns a builderd
//     microVM via the vmmd RPC. A wedged vmmd connection
//     surfaces immediately so the operator can re-route
//     builderd traffic to another node (PR-scale-out
//     multi-host work) before customers hit failed builds.
//   - /srv/fc/builds writable: the build-volume path. The
//     writer microVM's overlay mount source lives here.
//     Failing this check is a hard error; builderd will not
//     produce builds.
//
// Result cache: each check caches its verdict for 5 s so a 1 s
// Prometheus scrape cadence doesn't turn into 3 syscalls per
// daemon per second. The vmmd dial uses a 1 s deadline so a
// half-down vmmd can't stall the readiness loop.
package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// defaultBuildsDir is the canonical /srv/fc/builds path
// builderd uses for builder microVM overlays. Production
// sets cfg.BuildDriveDir / cfg.BuildExportDir at boot via
// the operator's TOML; the constant here is the fallback.
const defaultBuildsDir = "/srv/fc/builds"

// vmmdDialer is the subset of grpc.DialContext builderd uses
// to verify the vmmd connection. Defining it locally keeps
// the test path free of grpc imports.
type vmmdDialer func(ctx context.Context, target string) error

// pgPool is the subset of *pgxpool.Pool we need for the PG
// ping signal. Same shape as cmd/schedd/readiness.go::pgPool.
type pgPool interface {
	Ping(ctx context.Context) error
}

// BuildReadinessProbe constructs the builderd /readyz probe.
//
// storageRoot is the imaged-compatible root (typically /srv/fc);
// the probe checks `storageRoot/builds` writability. vmmTarget
// is the vmmd dial target (unix socket path or tcp://host:port);
// the probe dials it on every check with a 1 s deadline.
// pool is the *pgxpool.Pool used by the build worker; nil
// degrades the probe to a single "pg pool nil (test path)"
// signal.
func BuildReadinessProbe(ctx context.Context, pool pgPool, storageRoot, vmmTarget string, dial vmmdDialer) *wire.ReadyzProbe {
	if storageRoot == "" {
		storageRoot = defaultBuildsDir
	}
	buildsDir := filepath.Join(storageRoot, "builds")
	return buildReadinessProbeForDrive(ctx, pool, buildsDir, vmmTarget, dial)
}

func buildReadinessProbeForDrive(ctx context.Context, pool pgPool, buildsDir, vmmTarget string, dial vmmdDialer) *wire.ReadyzProbe {
	if dial == nil {
		// No dial seam: simulate via a TCP attempt. Production
		// passes deps.dialVmmd; tests inject a stub.
		dial = defaultDial
	}
	p := &wire.ReadyzProbe{}
	if pool != nil {
		sig, stop := wire.NewPGPingSignal(ctx, pool, 5*time.Second)
		p.RegisterSignal(sig, stop)
	} else {
		s := p.Register()
		s.Set(false, "pg pool nil (test path)")
	}
	vmmSig, vmmStop := vmmdDialSignal(ctx, vmmTarget, dial, 5*time.Second)
	p.RegisterSignal(vmmSig, vmmStop)
	buildsSig, buildsStop := buildsDirSignal(buildsDir, 5*time.Second)
	p.RegisterSignal(buildsSig, buildsStop)
	return p
}

// defaultDial is the production dial path: grpc.DialContext.
// Tests inject a stub via BuildReadinessProbe's dial param.
func defaultDial(ctx context.Context, target string) error {
	// Imported lazily so the cmd/builderd build doesn't pull
	// google.golang.org/grpc into every test that exercises
	// BuildReadinessProbe without a real vmmd.
	return dialGRPC(ctx, target)
}

// vmmdDialSignal reports whether vmmd is reachable. Each
// check dials vmmTarget with a 1 s deadline; the result is
// cached for cacheFor. Success: ready=true; failure: ready=
// false with the dial error in the reason.
func vmmdDialSignal(ctx context.Context, vmmTarget string, dial vmmdDialer, cacheFor time.Duration) (*wire.ReadySignal, func()) {
	s := &wire.ReadySignal{}
	s.Set(false, "vmmd not yet dialed")
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	stopper := func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
			s.Set(false, "builderd stopping")
		})
	}
	go func() {
		defer close(done)
		t := time.NewTicker(cacheFor)
		defer t.Stop()
		check := func() {
			if vmmTarget == "" {
				s.Set(false, "vmmd target empty")
				return
			}
			cctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := dial(cctx, vmmTarget); err != nil {
				s.Set(false, "vmmd dial failed: "+err.Error())
				return
			}
			s.Set(true, "")
		}
		check()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				check()
			}
		}
	}()
	return s, stopper
}

// buildsDirSignal reports whether path is writable. Same
// shape as cmd/imaged/readiness.go::checkWritable but
// specialised for a dir (the build-volume is always a dir).
func buildsDirSignal(path string, cacheFor time.Duration) (*wire.ReadySignal, func()) {
	s := &wire.ReadySignal{}
	s.Set(false, "builds dir not yet checked")
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	stopper := func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
			s.Set(false, "builderd stopping")
		})
	}
	go func() {
		defer close(done)
		t := time.NewTicker(cacheFor)
		defer t.Stop()
		check := func() {
			doneCh := make(chan struct{})
			go func() {
				defer close(doneCh)
				if err := checkDirWritable(path); err != nil {
					s.Set(false, err.Error())
					return
				}
				s.Set(true, "")
			}()
			select {
			case <-doneCh:
				return
			case <-time.After(time.Second):
				s.Set(false, "timeout")
			}
		}
		check()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				check()
			}
		}
	}()
	return s, stopper
}

// checkDirWritable creates a tempfile inside dir via
// os.CreateTemp and immediately removes it. The temp file's
// create+remove cycle proves the dir is writable without
// leaving anything on disk.
func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".builderd-readyz-*")
	if err != nil {
		return err
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(f.Name())
		return cerr
	}
	return os.Remove(f.Name())
}
