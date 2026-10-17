package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func crossesReleaseError() error {
	return &api.APIError{Problem: api.Problem{
		Status: 409, Code: api.CodeRollbackCrossesRelease,
		CrossedReleases: []api.RollbackCrossedRelease{{DeploymentID: "d42", Revision: 42, Status: "succeeded", StartedAt: "2026-10-15T10:02:13Z"}},
		RestorePoints: []api.ReleaseRestorePoint{{DatabaseID: "db-1", DatabaseName: "orders", PointInTime: "2026-10-15T10:02:13Z",
			Command: "gregale postgres restore db-1 --name orders-before-v42 --point-in-time 2026-10-15T10:02:13Z"}},
	}}
}

// TestSendRollbackOverReleases pins ADR-1448's CLI flow: the refusal is
// explained, --acknowledge-releases retries with acknowledgement, and a
// script without it gets the refusal back after one request.
func TestSendRollbackOverReleases(t *testing.T) {
	for _, tc := range []struct {
		name        string
		acknowledge bool
		wantCalls   int
		wantErr     bool
	}{
		{name: "acknowledged retries", acknowledge: true, wantCalls: 2},
		{name: "script without acknowledgement stops", acknowledge: false, wantCalls: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			oldStderr, oldNonInteractive, oldJSON := osStderr, nonInteractive, jsonOutput
			osStderr, nonInteractive, jsonOutput = &stderr, true, false
			t.Cleanup(func() { osStderr, nonInteractive, jsonOutput = oldStderr, oldNonInteractive, oldJSON })

			var calls []api.RollbackRequest
			_, err := sendRollbackOverReleases("my-api", api.RollbackRequest{}, tc.acknowledge, func(req api.RollbackRequest) (api.DeploymentResponse, error) {
				calls = append(calls, req)
				if !req.AcknowledgeReleases {
					return api.DeploymentResponse{}, crossesReleaseError()
				}
				return api.DeploymentResponse{ID: "d40"}, nil
			})
			if len(calls) != tc.wantCalls || (err != nil) != tc.wantErr {
				t.Fatalf("calls=%d err=%v; want %d calls, error=%v", len(calls), err, tc.wantCalls, tc.wantErr)
			}
			if tc.acknowledge && !calls[1].AcknowledgeReleases {
				t.Fatalf("retry did not acknowledge: %+v", calls[1])
			}
			if errors.Is(err, errRollbackDeclined) {
				t.Fatal("a script must get the API refusal, not a declined prompt")
			}
			for _, want := range []string{"v42  succeeded  started 2026-10-15T10:02:13Z", "gregale postgres restore db-1 --name orders-before-v42", "--acknowledge-releases"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestSendRollbackPassesOtherErrorsThrough(t *testing.T) {
	want := &api.APIError{Problem: api.Problem{Status: 409, Code: api.CodeRollbackTargetIneligible}}
	calls := 0
	_, err := sendRollbackOverReleases("my-api", api.RollbackRequest{}, true, func(api.RollbackRequest) (api.DeploymentResponse, error) {
		calls++
		return api.DeploymentResponse{}, want
	})
	if calls != 1 || !errors.Is(err, want) {
		t.Fatalf("calls=%d err=%v; want the original error after one call", calls, err)
	}
}

func TestReleaseSummaryLine(t *testing.T) {
	if got := releaseSummaryLine(nil); got != "" {
		t.Fatalf("no release = %q, want empty", got)
	}
	got := releaseSummaryLine(&api.DeploymentRelease{Status: "succeeded", StartedAt: "2026-10-15T10:02:13Z", FinishedAt: "2026-10-15T10:02:40Z",
		MigrationDatabases: []api.ReleaseDatabaseRef{{ID: "db-1", Name: "orders"}, {ID: "db-2", Name: "audit"}}})
	want := "release: succeeded, started 2026-10-15T10:02:13Z, finished 2026-10-15T10:02:40Z; can migrate orders, audit"
	if got != want {
		t.Fatalf("summary line = %q, want %q", got, want)
	}
}
