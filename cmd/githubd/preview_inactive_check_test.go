package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

type recordedCheckRun struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
}

// recordingChecks is a ChecksAPI whose check-run writes land in runs.
func recordingChecks(t *testing.T) (*githubd.ChecksAPI, *[]recordedCheckRun) {
	t.Helper()
	runs := &[]recordedCheckRun{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/octo/api/issues/42/comments" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.URL.Path != "/repos/octo/api/check-runs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var run recordedCheckRun
		if err := json.NewDecoder(r.Body).Decode(&run); err != nil {
			t.Errorf("decode check-run: %v", err)
		}
		*runs = append(*runs, run)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(fake.Close)
	target, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := githubd.NewChecksAPI(githubd.NewTokenCache(staticTokenFetcher{}, time.Minute),
		rewriteClient{base: fake.Client(), target: target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return checks, runs
}

// A commit that stopped being an open PR's head before its build finished
// (PR closed or merged mid-build, or a newer push) left its gregale-preview
// check queued or building forever.
func TestWriteInactivePreviewCheck(t *testing.T) {
	cases := []struct {
		name           string
		status         string
		check          previewSetCheck
		wantConclusion string // "" = no write
	}{
		{name: "closed PR head goes live", status: "live",
			check:          previewSetCheck{ClosedHead: true, Phase: githubdgrpc.CheckPhaseLive, Summary: "Preview PR #42 live.", RootSlug: "pr-42-demo"},
			wantConclusion: "success"},
		{name: "closed PR head fails", status: "failed",
			check:          previewSetCheck{ClosedHead: true, Phase: githubdgrpc.CheckPhaseFailed, Summary: "Preview PR #42 failed."},
			wantConclusion: "failure"},
		{name: "closed PR head still building", status: "building",
			check: previewSetCheck{ClosedHead: true, Phase: githubdgrpc.CheckPhaseBuilding}},
		{name: "superseded commit goes live", status: "live",
			check: previewSetCheck{SupersededBy: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, wantConclusion: "neutral"},
		{name: "superseded commit build cancelled", status: "cancelled",
			check: previewSetCheck{SupersededBy: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, wantConclusion: "neutral"},
		{name: "superseded commit still building", status: "building",
			check: previewSetCheck{SupersededBy: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		// It went live as the head and completed its check then.
		{name: "head that went live is later superseded", status: "superseded",
			check: previewSetCheck{SupersededBy: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		{name: "another open PR owns the commit", status: "live", check: previewSetCheck{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks, runs := recordingChecks(t)
			if err := writeInactivePreviewCheck(context.Background(), checks, 99, "octo/api",
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", tc.status, "gregale.dev", tc.check); err != nil {
				t.Fatalf("writeInactivePreviewCheck: %v", err)
			}
			if tc.wantConclusion == "" {
				if len(*runs) != 0 {
					t.Fatalf("check-runs = %+v, want none", *runs)
				}
				return
			}
			if len(*runs) != 1 || (*runs)[0].Status != "completed" || (*runs)[0].Conclusion != tc.wantConclusion {
				t.Fatalf("check-runs = %+v, want one completed %s", *runs, tc.wantConclusion)
			}
		})
	}
}

// A preview torn down while its build was queued or running completes the
// commit's preview check instead of leaving it pending.
func TestSyncTornDownPreviewCompletesUnfinishedCheck(t *testing.T) {
	checks, runs := recordingChecks(t)
	err := syncTornDownPreview(context.Background(), checks, tornDownPreview{
		deploymentID: "deployment-1", installationID: 99, repo: "octo/api", commitSHA: "deadbeef",
		environment: "preview/demo", appSlug: "pr-42-demo", prNumber: 42, unfinished: true,
	})
	if err != nil {
		t.Fatalf("syncTornDownPreview: %v", err)
	}
	if len(*runs) != 1 || (*runs)[0].Conclusion != "neutral" || (*runs)[0].Output.Title != "Preview torn down" {
		t.Fatalf("check-runs = %+v, want one neutral torn-down check", *runs)
	}
}
