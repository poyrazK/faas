package api

import (
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestNormalizeBulkIssueAction(t *testing.T) {
	now := time.Now()
	id := uuid.NewString()
	until := now.Add(time.Hour)
	for _, tc := range []struct {
		name  string
		in    BulkIssueActionRequest
		valid bool
	}{
		{"assign", BulkIssueActionRequest{[]string{id}, IssueActionRequest{Action: "assign"}}, true},
		{"ignore", BulkIssueActionRequest{[]string{id}, IssueActionRequest{Action: "ignore", IgnoredUntil: &until}}, true},
		{"no selection", BulkIssueActionRequest{nil, IssueActionRequest{Action: "reopen"}}, false},
		{"duplicate encoding", BulkIssueActionRequest{[]string{id, strings.ToUpper(id)}, IssueActionRequest{Action: "reopen"}}, false},
		{"nil identity", BulkIssueActionRequest{[]string{uuid.Nil.String()}, IssueActionRequest{Action: "reopen"}}, false},
		{"regrouping", BulkIssueActionRequest{[]string{id}, IssueActionRequest{Action: "merge", TargetIssueID: uuid.NewString()}}, false},
		{"extraneous field", BulkIssueActionRequest{[]string{id}, IssueActionRequest{Action: "reopen", FixedDeploymentID: uuid.NewString()}}, false},
		{"missing fixing release", BulkIssueActionRequest{[]string{id}, IssueActionRequest{Action: "resolve"}}, false},
		{"missing expiry", BulkIssueActionRequest{[]string{id}, IssueActionRequest{Action: "ignore"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeBulkIssueAction(tc.in, now)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	in := BulkIssueActionRequest{Action: IssueActionRequest{Action: "reopen"}}
	for i := 0; i < IssueBulkActionMax; i++ {
		in.IssueIDs = append(in.IssueIDs, uuid.NewString())
	}
	if _, err := NormalizeBulkIssueAction(in, now); err != nil {
		t.Fatal(err)
	}
	in.IssueIDs = append(in.IssueIDs, uuid.NewString())
	if _, err := NormalizeBulkIssueAction(in, now); err == nil {
		t.Fatal("oversized selection accepted")
	}
	input := []string{strings.ToUpper(id)}
	out, err := NormalizeBulkIssueAction(BulkIssueActionRequest{input, IssueActionRequest{Action: "reopen"}}, now)
	if err != nil || out.IssueIDs[0] != id || input[0] != strings.ToUpper(id) {
		t.Fatal("normalization changed caller selection")
	}
}
