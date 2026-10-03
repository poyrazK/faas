package imaged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/scanview"
	"github.com/onebox-faas/faas/pkg/state"
)

// Injected materialization/scanner fixtures exercise handoff and producer
// fences, not native mounts, real Grype, durable runtime approval or adoption.
type fixtureRuntimeScanOwner struct {
	source string
	mode   string
	target string
	calls  int
}

func (o *fixtureRuntimeScanOwner) MaterializeRuntimeScan(ctx context.Context, r runtimescan.Request) (runtimescan.Receipt, error) {
	o.calls++
	o.target = r.TargetDir
	if o.mode == "native failure" {
		return runtimescan.Receipt{}, errors.New("native cleanup failure")
	}
	hash, err := runtimeadmission.HashArtifactSources(r.Sources)
	if err != nil {
		return runtimescan.Receipt{}, err
	}
	receipt := runtimescan.Receipt{Version: 1, InputHash: r.InputHash, SourcesHash: hash, TargetDir: r.TargetDir}
	for _, source := range r.Sources {
		if source.Kind == "base-image" {
			continue
		}
		name := filepath.Join(r.TargetDir, runtimescan.ViewDirectory(source.WorkloadName))
		if err := os.Mkdir(name, 0700); err != nil {
			return runtimescan.Receipt{}, err
		}
		tree, err := scanview.Snapshot(ctx, o.source)
		if err != nil {
			return runtimescan.Receipt{}, err
		}
		actual, err := scanview.Copy(ctx, o.source, name, tree)
		if err != nil {
			return runtimescan.Receipt{}, err
		}
		receipt.Views = append(receipt.Views, runtimescan.View{WorkloadName: source.WorkloadName, SourceTree: tree, ProjectionTree: actual})
	}
	switch o.mode {
	case "incomplete":
		receipt.Views = receipt.Views[:1]
	case "wrong source":
		receipt.SourcesHash = strings.Repeat("f", 64)
	case "changed before scan":
		if err := os.WriteFile(filepath.Join(r.TargetDir, "main", "package"), []byte("changed"), 0600); err != nil {
			return runtimescan.Receipt{}, err
		}
	}
	return receipt, nil
}

type fixtureRuntimeScanStore struct {
	state.DeploymentRuntimeArtifactInputStore
	mode string
}

func (s fixtureRuntimeScanStore) GetFreshDeploymentRuntimeArtifactInputs(ctx context.Context, account, app, dep string) (state.DeploymentRuntimeArtifactInputs, error) {
	in, err := s.DeploymentRuntimeArtifactInputStore.GetFreshDeploymentRuntimeArtifactInputs(ctx, account, app, dep)
	if s.mode == "producer changed" {
		in.InputHash = strings.Repeat("f", 64)
	}
	if s.mode == "producer expired" {
		return in, state.ErrApplicationStandardRuntimeStale
	}
	return in, err
}

func TestProducedRuntimeScanBindsEveryViewAndFreshProducerAfterScanner(t *testing.T) {
	for _, mode := range []string{"complete", "native failure", "incomplete", "wrong source", "changed before scan", "changed during scan", "scanner failure", "nil scanner", "producer changed", "producer expired"} {
		t.Run(mode, func(t *testing.T) {
			h, th := producedScanFixtureWithBase(t, true, true)
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := h.runProducedDeploymentScans(t.Context(), th.app, th.dep, root); err != nil {
				t.Fatal(err)
			}
			inputs, err := th.store.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "package"), []byte("guest packages"), 0600); err != nil {
				t.Fatal(err)
			}
			owner := &fixtureRuntimeScanOwner{source: source, mode: mode}
			calls := 0
			scan := func(ctx context.Context, dir string) (*ScanResult, error) {
				calls++
				if mode == "scanner failure" {
					return nil, errors.New("scanner failed")
				}
				if mode == "nil scanner" {
					return nil, nil
				}
				if mode == "changed during scan" {
					if err := os.WriteFile(filepath.Join(dir, "package"), []byte("changed"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return producedScanResult(t, false), nil
			}
			result, err := scanProducedRuntime(t.Context(), fixtureRuntimeScanStore{th.store, mode}, owner, scan, inputs, t.TempDir())
			if (err == nil) != (mode == "complete") {
				t.Fatal("invalid runtime scan acquired evidence", mode, err)
			}
			if mode == "complete" && (len(result.Reports) != 2 || len(result.Materialization.Views) != 2 || calls != 2 || result.Inputs.InputHash != inputs.InputHash) {
				t.Fatal("main/sidecar reports lost private producer binding")
			}
			if err != nil && result.Materialization.Version != 0 {
				t.Fatal("failed scan retained a receipt")
			}
			if _, err := os.Stat(owner.target); !os.IsNotExist(err) {
				t.Fatal("runtime projection cleanup incomplete", err)
			}
		})
	}
}

func TestRuntimeDirectoryScannerCannotSubstituteNestedExt4(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "rootfs.ext4"), []byte("customer regular file, not an ext4 filesystem"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package"), []byte("guest package"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "grype-fixture")
	report := `{"matches":[],"descriptor":{"name":"grype","version":"0.116.0","db":{"status":{"valid":true,"schemaVersion":"v6.0.2","built":"2026-10-01T00:00:00Z"}}}}`
	script := "#!/bin/sh\nset -eu\ndir=${1#dir:}\ntest -f \"$dir/rootfs.ext4\"\ntest -f \"$dir/package\"\nprintf '%s' '" + report + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	result, err := RunRuntimeGrypeAt(t.Context(), bin, source)
	if err != nil || result == nil {
		t.Fatal("directory scan substituted a nested customer image", err)
	}
}
