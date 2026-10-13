package issues

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

// adr: 1117
func TestCauseGroupingPreservesVersionsAndSelectsUnderlyingFailure(t *testing.T) {
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Wrapper", Message: "wrapped", SourceKind: "exception", GroupingStrategy: CauseStrategy,
		Causes: []api.IssueCause{{ExceptionType: "DatabaseError", Message: "query 123 failed", ErrorCode: "DB_DOWN", Frames: []api.IssueFrame{{File: "/app/src/db.go", Function: "query", InApp: true}}}}}
	normalize := func(e api.IssueEvent) (api.IssueEvent, string) {
		t.Helper()
		n, fp, _, err := Normalize(e, now, api.PlanHobby.IssueLimits())
		if err != nil {
			t.Fatal(err)
		}
		return n, fp
	}
	n, fp := normalize(event)
	if fp != "298f0a7e2ac867a34fe326c1804916d93be1d64b56c95bde51ef1127a89b2a9b" {
		t.Fatalf("version 3 identity drift: %s", fp)
	}
	exp := ExplainGrouping(n, 3, fp)
	if exp == nil || exp.CauseIndex == nil || *exp.CauseIndex != 0 || exp.ExceptionType != "DatabaseError" {
		t.Fatalf("explanation: %+v", exp)
	}
	event.ExceptionType, event.Message = "OtherWrapper", "different text"
	event.Causes = append([]api.IssueCause{{ExceptionType: "Middle", Message: "middle"}}, event.Causes...)
	_, same := normalize(event)
	if same != fp {
		t.Fatal("wrappers affect identity")
	}
	event.Causes[1].ErrorCode = "DB_AUTH"
	_, other := normalize(event)
	if other == fp {
		t.Fatal("root codes collapsed")
	}
	event.CausesIncomplete = true
	n, incomplete := normalize(event)
	if incomplete == fp || ExplainGrouping(n, 3, incomplete).CauseIndex != nil {
		t.Fatal("partial chain claimed root")
	}
	for _, strategy := range []string{"legacy", FailureSiteStrategy} {
		event.GroupingStrategy = strategy
		_, with := normalize(event)
		event.Causes = nil
		_, without := normalize(event)
		if with != without {
			t.Fatal("existing fingerprint changed")
		}
	}
	event.GroupingStrategy = CauseStrategy
	event.FingerprintOverride = "shared"
	_, override := normalize(event)
	event.Causes = []api.IssueCause{{ExceptionType: "Inner", Message: "inner"}}
	event.CausesIncomplete = false
	n, same = normalize(event)
	if same != override || ExplainGrouping(n, 3, same).CauseIndex != nil {
		t.Fatal("cause bypassed override")
	}
}

func TestCauseEvidenceIsBoundedSanitizedAndCopied(t *testing.T) {
	now := time.Now().UTC()
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Outer", GroupingStrategy: CauseStrategy,
		Causes: []api.IssueCause{{ExceptionType: "Inner", Message: "customer@example.com", Frames: []api.IssueFrame{{File: "/app/inner", Function: "read", InApp: true}}}}}
	n, fp, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(n.Causes[0].Message, "customer@example.com") || event.Causes[0].Message != "customer@example.com" {
		t.Fatal("cause sanitization/copy failed")
	}
	n.Causes[0].Frames[0].Function = "tampered"
	if ExplainGrouping(n, 3, fp) != nil {
		t.Fatal("unverified cause explanation")
	}
	event.Causes = make([]api.IssueCause, api.IssueMaxCauses+1)
	if _, _, _, err = Normalize(event, now, api.PlanHobby.IssueLimits()); err == nil {
		t.Fatal("unbounded causes accepted")
	}
	event.Causes = []api.IssueCause{{ExceptionType: "Inner", Frames: []api.IssueFrame{{Line: -1}}}}
	if _, _, _, err = Normalize(event, now, api.PlanHobby.IssueLimits()); err == nil {
		t.Fatal("invalid cause frame accepted")
	}
}
