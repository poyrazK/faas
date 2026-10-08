package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/devpatch"
)

type devPatchScript struct {
	polls    []devPatchPoll
	errs     []error
	asked    []int64
	applied  []string
	restarts int
	delays   []time.Duration
	applyErr error
}

func (s *devPatchScript) io() devPatchIO {
	return devPatchIO{
		fetch: func(after int64) (devPatchPoll, error) {
			s.asked = append(s.asked, after)
			i := len(s.asked) - 1
			if i < len(s.errs) && s.errs[i] != nil {
				return devPatchPoll{}, s.errs[i]
			}
			if i >= len(s.polls) {
				return devPatchPoll{Error: "dev_patch_disabled"}, nil
			}
			return s.polls[i], nil
		},
		apply: func(dir string, archive []byte, _ []string) (devpatch.ApplyResult, error) {
			s.applied = append(s.applied, dir+":"+string(archive))
			return devpatch.ApplyResult{Written: 1}, s.applyErr
		},
		restart: func() error { s.restarts++; return nil },
		sleep: func(_ context.Context, d time.Duration) bool {
			s.delays = append(s.delays, d)
			return true
		},
	}
}

func wirePatch(generation int64, content string) *devPatchWire {
	sum := sha256.Sum256([]byte(content))
	return &devPatchWire{Generation: generation, ImageDir: "/app", Archive: []byte(content), Digest: hex.EncodeToString(sum[:])}
}

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestDevPatchLoopAppliesNewGenerationsAndRestarts(t *testing.T) {
	script := &devPatchScript{polls: []devPatchPoll{
		{Unchanged: true},
		{Patch: wirePatch(1, "one")},
		{Unchanged: true},
		{Patch: wirePatch(3, "three")},
	}}
	runDevPatchLoop(context.Background(), quietLog, script.io())
	if want := []int64{0, 0, 1, 1, 3}; !equalInt64(script.asked, want) {
		t.Fatalf("generations asked = %v, want %v", script.asked, want)
	}
	if len(script.applied) != 2 || script.applied[0] != "/app:one" || script.applied[1] != "/app:three" || script.restarts != 2 {
		t.Fatalf("applied = %v restarts = %d", script.applied, script.restarts)
	}
}

func TestDevPatchLoopStopsWhenDisabledOrUnsupported(t *testing.T) {
	for _, reason := range []string{"dev_patch_disabled", "invalid_request", "unsupported_scope"} {
		script := &devPatchScript{polls: []devPatchPoll{{Error: reason}, {Patch: wirePatch(1, "late")}}}
		runDevPatchLoop(context.Background(), quietLog, script.io())
		if len(script.asked) != 1 || len(script.applied) != 0 {
			t.Fatalf("%s: asked %v applied %v, want one poll and no apply", reason, script.asked, script.applied)
		}
	}
}

func TestDevPatchLoopRejectsTamperedPatches(t *testing.T) {
	tampered := wirePatch(1, "one")
	tampered.Archive = []byte("other")
	wrongDir := wirePatch(2, "two")
	wrongDir.ImageDir = "/etc"
	script := &devPatchScript{polls: []devPatchPoll{{Patch: tampered}, {Patch: wrongDir}}}
	runDevPatchLoop(context.Background(), quietLog, script.io())
	if len(script.applied) != 0 || script.restarts != 0 {
		t.Fatalf("applied %v restarts %d, want tampered patches rejected", script.applied, script.restarts)
	}
	if want := []int64{0, 1, 2}; !equalInt64(script.asked, want) {
		t.Fatalf("generations asked = %v, want rejected generations skipped (%v)", script.asked, want)
	}
}

func TestDevPatchLoopFailedApplyDoesNotRestart(t *testing.T) {
	script := &devPatchScript{polls: []devPatchPoll{{Patch: wirePatch(1, "one")}}, applyErr: errors.New("disk full")}
	runDevPatchLoop(context.Background(), quietLog, script.io())
	if len(script.applied) != 1 || script.restarts != 0 {
		t.Fatalf("applied %v restarts %d, want no restart after a failed apply", script.applied, script.restarts)
	}
}

func TestDevPatchLoopBacksOffOnTransportErrors(t *testing.T) {
	boom := errors.New("vsock")
	script := &devPatchScript{
		polls: []devPatchPoll{{}, {}, {}, {Error: "dev_patch_unavailable"}, {Unchanged: true}},
		errs:  []error{boom, boom, boom},
	}
	runDevPatchLoop(context.Background(), quietLog, script.io())
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, time.Second}
	if len(script.delays) < len(want) {
		t.Fatalf("delays = %v", script.delays)
	}
	for i, d := range want {
		if script.delays[i] != d {
			t.Fatalf("delays = %v, want prefix %v", script.delays, want)
		}
	}
	if devPatchBackoff(50) != devPatchMaxBackoff {
		t.Fatalf("backoff is not capped: %s", devPatchBackoff(50))
	}
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
