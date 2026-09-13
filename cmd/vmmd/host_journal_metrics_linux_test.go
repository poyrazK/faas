//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHostJournalMetricsCountsAllocatedSparseBytes(t *testing.T) {
	dir := t.TempDir()
	sparse := filepath.Join(dir, "sparse.journal")
	const apparentBytes = 32 << 20
	if err := os.Truncate(sparse, apparentBytes); err != nil {
		t.Fatal(err)
	}

	originalDirs := hostJournalDirs
	hostJournalDirs = []string{dir}
	t.Cleanup(func() { hostJournalDirs = originalDirs })

	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "journalctl" {
			return nil, nil
		}
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	m := newHostJournalMetrics(prometheus.NewRegistry(), run, nil)
	if err := m.sample(context.Background()); err != nil {
		t.Fatal(err)
	}

	allocated := testutil.ToFloat64(m.journalBytes)
	if allocated >= apparentBytes {
		t.Fatalf("allocated journal bytes = %.0f, want less than sparse apparent size %d", allocated, apparentBytes)
	}
}
