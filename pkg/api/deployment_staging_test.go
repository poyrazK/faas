package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentPolicyIntentValidationAndFingerprint(t *testing.T) {
	r := DeploymentStagingRequest{SessionID: strings.Repeat("a", 32), Operation: "stage", ExpectedAppID: "app-1", ExpectedRevision: 3,
		Before: map[string]json.RawMessage{"profiling": json.RawMessage(`null`)}, Desired: map[string]json.RawMessage{"profiling": json.RawMessage(`{"enabled":true,"window_seconds":10}`)}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	a, err := r.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	r.Operation = "restore"
	r.Desired["profiling"] = json.RawMessage(" { \"window_seconds\": 10, \"enabled\": true } ")
	b, err := r.Fingerprint()
	if err != nil || a != b {
		t.Fatalf("non-canonical intent: %s != %s err=%v", a, b, err)
	}
	for _, raw := range []string{`{"enabled":true,"token":"secret"}`, `{"enabled":"true"}`, `{} {}`, `{"window_seconds":1.5}`} {
		r.Desired["profiling"] = json.RawMessage(raw)
		if err := r.Validate(); err == nil {
			t.Fatalf("unsafe policy accepted: %s", raw)
		}
	}
	r.Desired = map[string]json.RawMessage{"env": json.RawMessage(`{"KEY":"secret"}`)}
	if err := r.Validate(); err == nil {
		t.Fatal("environment map accepted")
	}
}

func TestDeploymentSubmissionBoundaryBeforeNetwork(t *testing.T) {
	for _, mode := range []string{"image", "multipart", "source-ref", "source-tarball", "dev-source", "commit"} {
		t.Run(mode, func(t *testing.T) {
			calls, boundaries := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = json.NewEncoder(w).Encode(DeploymentResponse{ID: "dep-1"})
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "fixture").SetCompletionCache(nil)
			diskFault := errors.New("journal write failed")
			ctx := ContextWithDeploymentSubmissionBoundary(t.Context(), func() error { boundaries++; return diskFault })
			var err error
			switch mode {
			case "image":
				_, err = c.Deploy(ctx, "demo", CreateDeploymentRequest{Image: "example/image@sha256:abc"})
			case "multipart":
				_, err = c.DeployMultipart(ctx, "demo", strings.NewReader("source"), "src.tar.gz", "node22", "", false, DeployAnnotations{})
			case "source-ref":
				_, err = c.DeployFromSourceRef(ctx, "demo", SourceRefDeployRequest{Repo: "o/r", Ref: "main"})
			case "source-tarball":
				_, err = c.DeployFromSourceTarball(ctx, "demo", strings.NewReader("source"), "src.tar.gz", SourceTarballDeployRequest{})
			case "dev-source":
				_, err = c.DeployDevSource(ctx, "demo", strings.NewReader("source"), "src.tar.gz", "node22", "", false, "", DeployAnnotations{}, "", "revision", nil)
			case "commit":
				_, err = c.CommitUpload(ctx, "upload-1")
			}
			if !errors.Is(err, diskFault) || calls != 0 || boundaries != 1 {
				t.Fatalf("submission escaped boundary: calls=%d boundaries=%d err=%v", calls, boundaries, err)
			}
		})
	}
}

func TestDeploymentBoundaryDoesNotFireForPolicyStaging(t *testing.T) {
	boundaries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(DeploymentStagingReceipt{Status: "staged"})
	}))
	defer srv.Close()
	ctx := ContextWithDeploymentSubmissionBoundary(t.Context(), func() error { boundaries++; return errors.New("unexpected submission") })
	_, err := NewClient(srv.URL, "fixture").ApplyDeploymentStaging(ctx, "demo", DeploymentStagingRequest{})
	if err != nil || boundaries != 0 {
		t.Fatalf("staging counted as deployment submission: %d err=%v", boundaries, err)
	}
}
