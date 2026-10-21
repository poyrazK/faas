package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectSubmissionExpiredHeartbeat(t *testing.T) {
	store := NewMemStore()
	id := strings.Repeat("a", 32)
	payload, _ := json.Marshal(api.ProjectSubmissionReceipt{AttemptID: id, ProjectSlug: "demo", Status: "processing"})
	store.projectSubmissions = map[string]projectSubmissionEntry{"owner\x00" + id: {Payload: payload, Status: "processing", UpdatedAt: time.Now().Add(-2 * time.Minute)}}
	receipt, err := store.ProjectSubmission(t.Context(), "owner", id)
	if err != nil || receipt.Status != "interrupted" {
		t.Fatalf("expired=%+v %v", receipt, err)
	}
	// A resumed heartbeat can still prove that the request is running; reads do not mutate status.
	if err := store.TouchProjectSubmission(t.Context(), "owner", id); err != nil {
		t.Fatal(err)
	}
	receipt, err = store.ProjectSubmission(t.Context(), "owner", id)
	if err != nil || receipt.Status != "processing" {
		t.Fatalf("heartbeat=%+v %v", receipt, err)
	}
}
