package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIssueSplitManagementClientPreservesReviewedDestination(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const rule = "22222222-2222-4222-8222-222222222222"
	const target = "33333333-3333-4333-8333-333333333333"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/apps/payments/issues/" + source + "/split-preview":
			_ = json.NewEncoder(w).Encode(IssueSplitPreview{OriginalIssueID: source, CanonicalIssueID: source, Matcher: IssueSplitMatcher{Route: "/checkout"}, MatchingEventCount: 2, EligibleEventCount: 1, ShadowedEventCount: 1, EligibleEventIDs: []string{target}, ApplicationStatus: "new-rule"})
		case "/v1/apps/payments/issues/" + source + "/split-rules/" + rule + "/undo":
			var in IssueSplitRuleUndoRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.CanonicalSourceIssueID != source {
				t.Error("lost reviewed destination")
			}
			_ = json.NewEncoder(w).Encode(IssueSplitRule{ID: rule, SourceIssueID: source, TargetIssueID: target, Priority: 1, Enabled: false, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.PreviewIssueSplit(context.Background(), "payments", source, IssueSplitMatcher{Route: "/checkout"})
	if err != nil || preview.EligibleEventCount != 1 || preview.ShadowedEventCount != 1 || preview.ApplicationStatus != "new-rule" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	undone, err := client.UndoIssueSplitRule(context.Background(), "payments", source, rule, IssueSplitRuleUndoRequest{CanonicalSourceIssueID: source})
	if err != nil || undone.Enabled || undone.TargetIssueID != target {
		t.Fatalf("undo: %+v %v", undone, err)
	}
}
