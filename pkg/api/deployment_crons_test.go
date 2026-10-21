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
