package imaged

// adr: 435. Injected mkfs/native/Grype fixtures, not native acceptance.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeDefaultBaseAbsentStore struct{ state.Store }

func TestRuntimeDefaultBaseRequiresCurrentIntentBootLayoutAndBytes(t *testing.T) {
	for _, mode := range []string{"complete", "storage drift", "guest init drift", "configured source changed", "missing runtime", "missing producer", "tag resolved", "tag unavailable", "pinned registry outage"} {
		t.Run(mode, func(t *testing.T) {
			h, _, puller, _, ref := verifiedBaseFixture(t, "complete")
			h.WithDeployBaseRef(ref)
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			key := sched.BaseKeyForArch("", oci.ImageArchitecture)
			base, err := h.EnsureBaseExt4(t.Context(), ref, key, "base/default.digest", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			runtime := ""
			switch mode {
			case "storage drift":
				be, _ := h.storageFor()
				if err := be.Put(t.Context(), key, strings.NewReader("changed base bytes")); err != nil {
					t.Fatal(err)
				}
			case "guest init drift":
				if err := os.WriteFile(h.guestInitPath, []byte("changed init"), 0755); err != nil {
					t.Fatal(err)
				}
			case "configured source changed":
				h.WithDeployBaseRef("registry.example/base@sha256:" + strings.Repeat("a", 64))
			case "missing runtime":
				runtime = "node22"
			case "missing producer":
				h.store = runtimeDefaultBaseAbsentStore{h.store}
			case "tag resolved", "tag unavailable":
				h.WithDeployBaseRef("registry.example/base:development")
				if mode == "tag unavailable" {
					puller.resolveErr = errors.New("injected base registry outage")
				}
			case "pinned registry outage":
				puller.resolveErr = errors.New("injected base registry outage")
			}
			got, err := h.runtimeDefaultBaseProducer(t.Context(), runtime)
			if mode == "complete" || mode == "tag resolved" || mode == "pinned registry outage" {
				if err != nil || got.ID != base.Producer.ID || got.Input.Artifact.StorageKey != key {
					t.Fatal("default base identity lost", err)
				}
			} else if err == nil {
				t.Fatal("unproved runtime-default base accepted")
			}
		})
	}
}

func TestRuntimeDefaultBaseFullPipelineScansEverySeparateSource(t *testing.T) {
	for _, sidecar := range []bool{false, true} {
		t.Run(map[bool]string{false: "main", true: "main and sidecar"}[sidecar], func(t *testing.T) {
			h, th := producedScanFixture(t, sidecar)
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "package"), []byte("simulated package"), 0600); err != nil {
				t.Fatal(err)
			}
			owner := &pipelineRuntimeVMM{nopVMMClient: &nopVMMClient{}, fixtureRuntimeScanOwner: &fixtureRuntimeScanOwner{source: source}}
			h.WithVMMClient(owner)
			h.runtimeScanParent = t.TempDir()
			h.WithRuntimeGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
			if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
				t.Fatal(err)
			}
			value, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			want := 2
			if sidecar {
				want++
			}
			if err != nil || len(value.Scan.Input.Artifacts) != want || len(value.Scan.Input.Reports) != want-1 || owner.calls != 1 {
				t.Fatal("full-rootfs omitted a runtime source", err)
			}
			main := value.Scan.Input.Artifacts[1]
			if main.Kind != "full-rootfs" || main.BaseProducerID != value.Scan.Input.Artifacts[0].ProducerID || main.StorageKey == value.Scan.Input.Artifacts[0].StorageKey {
				t.Fatal("full-rootfs flattened or lost its default base")
			}
		})
	}
}
