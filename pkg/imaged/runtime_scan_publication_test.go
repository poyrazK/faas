package imaged

// adr: 435. Real private storage, injected materializer/Grype; not native proof.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func producedRuntimePublicationFixture(t *testing.T) (*testHarness, ProducedRuntimeScan) {
	t.Helper()
	_, th := producedScanFixtureWithBase(t, true, true)
	inputs, err := th.store.GetFreshDeploymentRuntimeProducerInputs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "package"), []byte("guest packages"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := &fixtureRuntimeScanOwner{source: source}
	result, err := scanProducedRuntime(t.Context(), th.store, owner, func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil }, inputs, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owner.target); !os.IsNotExist(err) {
		t.Fatal("projection retained at durable publication", err)
	}
	return th, result
}

func TestProducedRuntimePublicationRechecksCurrentFactsAfterCleanup(t *testing.T) {
	for _, mode := range []string{"complete", "high findings", "invalid scanner", "missing report", "extra report", "producer replaced", "sidecar replaced", "base replaced", "publisher revoked"} {
		t.Run(mode, func(t *testing.T) {
			th, result := producedRuntimePublicationFixture(t)
			switch mode {
			case "high findings":
				result.Reports[""] = producedScanResult(t, true)
			case "invalid scanner":
				result.Reports[""].ScannerName = "unknown"
			case "missing report":
				delete(result.Reports, "metrics")
			case "extra report":
				result.Reports["extra"] = producedScanResult(t, false)
			default:
				mutateRuntimeScanProducerFixture(t, t.Context(), th, result.Inputs, mode)
			}
			value, err := publishProducedRuntimeScan(t.Context(), th.store, result)
			valid := mode == "complete" || mode == "high findings"
			if (err == nil) != valid {
				t.Fatal("invalid scan acquired durable facts", mode, err)
			}
			if !valid {
				return
			}
			current, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			if err != nil || current.Scan.ID != value.ID || len(value.Input.Reports) != 2 || len(value.Input.Facts.Views) != 2 {
				t.Fatal("durable main/sidecar facts lost binding", err)
			}
			if mode == "high findings" && current.Scan.Input.Reports[0].Report.SeverityCounts.High != 1 {
				t.Fatal("high finding was discarded")
			}
			if _, err := th.store.GetFreshDeploymentRuntimeArtifactInputs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
				t.Fatal("publication conferred native/component approval", err)
			}
			if err := publishRuntimeScanFailure(t.Context(), th.store, result.Inputs, "scanner_invalid"); err != nil {
				t.Fatal(err)
			}
			if _, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
				t.Fatal("failed rescan preserved fresh success", err)
			}
		})
	}
}

func TestProducedRuntimeFailurePublicationCannotInvalidateReplacement(t *testing.T) {
	th, result := producedRuntimePublicationFixture(t)
	mutateRuntimeScanProducerFixture(t, t.Context(), th, result.Inputs, "producer replaced")
	if err := publishRuntimeScanFailure(t.Context(), th.store, result.Inputs, "scanner_unavailable"); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatal("stale failure selected over replacement", err)
	}
	if _, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("refused failure fabricated history", err)
	}
	if got := runtimeScanFailureCode(runtimeadmission.ErrInvalid); got != "scanner_invalid" {
		t.Fatal(got)
	}
}

func TestRuntimeScanPublicationJobRecordsUnavailableNativeOwner(t *testing.T) {
	h, th := producedScanFixtureWithBase(t, false, true)
	if _, err := h.ScanAndPublishProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatal("missing native owner produced success", err)
	}
	current, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || current.Input.Status != "failed" || current.Input.Failure != "scanner_unavailable" || len(current.Input.Reports) != 0 {
		t.Fatal("unavailable native owner did not publish bounded failure", err)
	}
	if _, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatal("failed private job conferred fresh approval", err)
	}
}
