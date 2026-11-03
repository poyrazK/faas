package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDeploymentCronIntentValidation(t *testing.T) {
	row := DeploymentCronConfig{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/tick", Enabled: true, Timezone: "UTC"}
	req := DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: uuid.NewString(), Before: []DeploymentCronConfig{row}, Desired: []DeploymentCronConfig{}}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	digest := req.Fingerprint()
	req.Operation = "restore"
	if req.Fingerprint() != digest {
		t.Fatal("operation changed intent digest")
	}
	req.Desired = nil
	if req.Validate() == nil {
		t.Fatal("null set accepted")
	}
	req.Desired = []DeploymentCronConfig{row, row}
	if req.Validate() == nil {
		t.Fatal("duplicate set accepted")
	}
	req.Desired = []DeploymentCronConfig{row}
	req.Desired[0].Schedule = "bad"
	if req.Validate() == nil {
		t.Fatal("invalid schedule accepted")
	}
}
// Staged crons follow the cron API's path and timezone rules; a cron carried
// unchanged, and an intent being undone, keep the rules they were stored
// under (crons audit 2026-11-03).
func TestDeploymentCronStagingAppliesCronAPIRules(t *testing.T) {
	legacy := DeploymentCronConfig{ID: uuid.NewString(), Schedule: "0 * * * *", Path: "/old path", Enabled: true, Timezone: "Local"}
	for _, tc := range []struct {
		name, path, timezone string
		operation            string
		ok                   bool
	}{
		{name: "valid", path: "/tick?x=1", timezone: "Europe/Istanbul", operation: "stage", ok: true},
		{name: "protocol-relative", path: "//evil.example/x", timezone: "UTC", operation: "stage"},
		{name: "space", path: "/a b", timezone: "UTC", operation: "prepare"},
		{name: "fragment", path: "/a#b", timezone: "UTC", operation: "stage"},
		{name: "too long", path: "/" + strings.Repeat("a", CronPathMaxBytes), timezone: "UTC", operation: "stage"},
		{name: "host-local zone", path: "/tick", timezone: "Local", operation: "stage"},
		{name: "restore replays", path: "/a b", timezone: "Local", operation: "restore", ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := DeploymentCronConfig{ID: uuid.NewString(), Schedule: "0 * * * *", Path: tc.path, Enabled: true, Timezone: tc.timezone}
			req := DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: tc.operation, ExpectedAppID: uuid.NewString(),
				Before: []DeploymentCronConfig{legacy}, Desired: []DeploymentCronConfig{legacy, row}}
			if err := req.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	changed := legacy
	changed.Path = "/still bad"
	req := DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: uuid.NewString(),
		Before: []DeploymentCronConfig{legacy}, Desired: []DeploymentCronConfig{changed}}
	if req.Validate() == nil {
		t.Fatal("a changed legacy cron skipped the path rules")
	}
}

func TestDeploymentBoundaryDoesNotFireForCronStaging(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(DeploymentStagingReceipt{Status: "staged"})
	}))
	defer srv.Close()
	ctx := ContextWithDeploymentSubmissionBoundary(t.Context(), func() error { calls++; return nil })
	if _, err := NewClient(srv.URL, "fixture").ApplyDeploymentCronStaging(ctx, "demo", DeploymentCronStagingRequest{}); err != nil || calls != 0 {
		t.Fatalf("boundary=%d err=%v", calls, err)
	}
}

func TestDeploymentCronTracksOpaqueUploadIdentity(t *testing.T) {
	request := DeploymentCronStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "prepare", ExpectedAppID: uuid.NewString(), Before: []DeploymentCronConfig{}, Desired: []DeploymentCronConfig{}}
	digest := request.Fingerprint()
	request.Operation = "track_upload"
	request.UploadID = "opaque_-Upload123"
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	if request.Fingerprint() != digest {
		t.Fatal("registering an upload changed the immutable cron intent")
	}
	for _, id := range []string{"", "../other", "a/b", "with space", "é", strings.Repeat("a", 129)} {
		request.UploadID = id
		if request.Validate() == nil {
			t.Errorf("invalid upload identity accepted: %q", id)
		}
	}
	request.Operation = "prepare"
	request.UploadID = "opaque"
	if request.Validate() == nil {
		t.Fatal("prepare accepted a mutable upload binding")
	}
}
