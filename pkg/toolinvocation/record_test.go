package toolinvocation_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func queued(t *testing.T) api.ToolInvocationRecord {
	t.Helper()
	intent := api.ToolInvocationIntent{Version: strings.Repeat("a", 64), Name: "echo", ArgumentsSHA256: toolpublication.Digest([]byte(`{}`)), AccessMode: "open", TimeoutSeconds: 10, AllowToolExecution: true}
	target := api.ToolInvocationTarget{App: "tools", AppID: "app", DeploymentID: "revision", DeploymentSourceSHA256: strings.Repeat("b", 64), CatalogSHA256: strings.Repeat("c", 64), CatalogContractSHA256: strings.Repeat("d", 64), EndpointPath: "/mcp"}
	record, err := toolinvocation.New("account", uuid.NewString(), intent, target, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestInvocationDispatchAndCancellationFacts(t *testing.T) {
	q := queued(t)
	now := q.CreatedAt.Add(time.Millisecond)
	first, err := toolinvocation.Change(q, "dispatch", nil, now)
	if err != nil || !first.DispatchGranted || first.Record.Status != "running" {
		t.Fatal("first dispatch was not granted")
	}
	duplicate, err := toolinvocation.Change(first.Record, "dispatch", nil, now)
	if err != nil || duplicate.DispatchGranted {
		t.Fatal("dispatch grant replayed")
	}
	cancel, err := toolinvocation.Change(first.Record, "cancel", nil, now)
	if err != nil || cancel.Record.Status != "running" || !cancel.Record.CancellationRequested {
		t.Fatal("cancellation asserted a rollback")
	}
	out, err := toolinvocation.Result([]byte(`{"content":[],"structuredContent":{"n":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	complete, err := toolinvocation.Change(cancel.Record, "finish", &out, now)
	if err != nil || complete.Record.Status != "completed" {
		t.Fatal("received result was discarded after cancellation")
	}
	if _, err = toolinvocation.Change(complete.Record, "finish", &out, now); err != nil {
		t.Fatal("identical finish was not idempotent")
	}
	other := out
	other.ResultSHA256 = strings.Repeat("f", 64)
	if _, err = toolinvocation.Change(complete.Record, "finish", &other, now); err == nil {
		t.Fatal("completed evidence overwritten")
	}
	before, err := toolinvocation.Change(q, "cancel", nil, now)
	if err != nil || before.Record.Status != "cancelled" || before.Record.DispatchedAt != nil {
		t.Fatal("queued cancellation did not prevent dispatch")
	}
	after, err := toolinvocation.Change(before.Record, "dispatch", nil, now)
	if err != nil || after.DispatchGranted {
		t.Fatal("cancelled request dispatched")
	}
	if got := toolinvocation.View(q, q.Deadline); got.Status != "not_started" {
		t.Fatal("expired queue implied side effects")
	}
	if got := toolinvocation.View(first.Record, q.Deadline); got.Status != "outcome_unknown" {
		t.Fatal("lost running request became retryable")
	}
	if grant, _ := toolinvocation.Change(q, "dispatch", nil, q.Deadline); grant.DispatchGranted {
		t.Fatal("expired request dispatched")
	}
}

func TestInvocationArgumentAndResultPrivacy(t *testing.T) {
	canonical, err := toolinvocation.CanonicalArguments([]byte(" { \"n\":9007199254740993, \"a\": 1 } "))
	if err != nil || string(canonical) != `{"a":1,"n":9007199254740993}` {
		t.Fatalf("argument precision: %s %v", canonical, err)
	}
	for _, raw := range []string{"null", "[]", "{} {}", strings.Repeat(" ", api.ToolInvocationArgsMaxBytes+1)} {
		if _, err := toolinvocation.CanonicalArguments([]byte(raw)); err == nil {
			t.Fatal("invalid argument object accepted")
		}
	}
	result, err := toolinvocation.Result([]byte(`{"content":[{"type":"text","text":"private-token fp_test_fake"}],"structuredContent":{"n":9007199254740993},"_meta":{"secret":"never-retain"},"requestState":"private-state"}`), "private-token")
	if err != nil || !result.Redacted || bytes.Contains(result.Result, []byte("private-token")) || bytes.Contains(result.Result, []byte("never-retain")) || !bytes.Contains(result.Result, []byte("9007199254740993")) {
		t.Fatalf("unsafe result: %s %v", result.Result, err)
	}
	large, err := toolinvocation.Result([]byte(`{"content":[{"type":"text","text":"` + strings.Repeat("x", api.ToolInvocationResultMaxBytes) + `"}]}`))
	if err != nil || !large.ResultOmitted || len(large.Result) != 0 || toolinvocation.ValidateOutcome(large) != nil {
		t.Fatal("large result exceeded journal bound")
	}
	for _, kind := range []string{"task", "input_required"} {
		if _, err := toolinvocation.Result([]byte(`{"resultType":"` + kind + `","content":[]}`)); err == nil {
			t.Fatal("unsupported continuation silently resumed")
		}
	}
}
