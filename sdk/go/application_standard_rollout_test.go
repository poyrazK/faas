package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApplicationStandardSDKRolloutControls(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	stamp, err := time.Parse(time.RFC3339Nano, "2026-10-04T12:00:00.123456Z")
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		want := map[string]any{"expected_updated_at": stamp.Format(time.RFC3339Nano)}
		if strings.HasSuffix(r.URL.Path, "/approve") {
			want = map[string]any{"approval_hash": hash}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("lost exact approval/control token: %+v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ApplicationStandardOperation{ID: id, State: "waiting", UpdatedAt: stamp, Targets: []ApplicationStandardOperationTarget{{AppID: id, State: "persisted", DesiredRevision: 2}}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	op, err := client.ApproveApplicationStandardReview(context.Background(), "acme", id, ApproveApplicationStandardReviewRequest{ApprovalHash: hash})
	if err != nil || op.State != "waiting" || !op.UpdatedAt.Equal(stamp) {
		t.Fatalf("approval response: %+v %v", op, err)
	}
	for _, control := range []func(context.Context, string, string, ControlApplicationStandardOperationRequest) (ApplicationStandardOperation, error){client.PauseApplicationStandardOperation, client.ResumeApplicationStandardOperation, client.AbortApplicationStandardOperation} {
		op, err = control(context.Background(), "acme", id, ControlApplicationStandardOperationRequest{ExpectedUpdatedAt: stamp})
		if err != nil || op.Targets[0].State != "persisted" || !op.UpdatedAt.Equal(stamp) {
			t.Fatalf("control response: %+v %v", op, err)
		}
	}
	base := "POST /v1/orgs/acme/application-standard-operations/" + id
	want := []string{"POST /v1/orgs/acme/application-standard-reviews/" + id + "/approve", base + "/pause", base + "/resume", base + "/abort"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths %v", paths)
	}
}
