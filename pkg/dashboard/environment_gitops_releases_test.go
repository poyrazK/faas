package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGitOpsRecoveryDashboardShowsEligibilityAndEscapesBlockers(t *testing.T) {
	now := time.Now().UTC()
	data := EnvironmentGitOpsData{Project: "shop", Environment: "production", Status: &api.EnvironmentGitOpsStatusResponse{}, Recovery: &api.ListEnvironmentGitReleasesResponse{Project: "shop", Environment: "production", TargetCommitSHA: strings.Repeat("a", 40), ObservedAt: now, NextBefore: "cursor", Releases: []api.EnvironmentGitRecoveryRelease{{ReleaseSetID: "release", CommitSHA: "old", ServedAt: &now, ExpiresAt: &now, RetentionStatus: "compatibility_window_elapsed", Eligibility: "blocked", BlockingReasons: []string{"<script>private</script>"}}}}}

	until := now.Add(time.Hour)
	data.Recovery.RecoveryRetention = &api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: 1000}
	data.Recovery.RetentionBytes = 100
	data.Recovery.Releases[0].ProtectionStatus = "protected"
	data.Recovery.Releases[0].ArtifactsProtectedUntil = &until
	data.Recovery.Releases[0].RetainedBytes = 100
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "", Page{Title: "GitOps", Body: "environment_gitops", Data: data}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Artifact protection: protected (100 bytes)", "Currently charged: 100 bytes", "Recovery releases", `method="GET"`, "Blocked", "compatibility_window_elapsed", "do not guarantee artifact retention", "rollback-plan shop production --to-release release", "Older recovery releases", "&lt;script&gt;private&lt;/script&gt;"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatal("missing", want)
		}
	}
	if strings.Contains(rec.Body.String(), "<script>private") {
		t.Fatal("unescaped blocker")
	}
}
