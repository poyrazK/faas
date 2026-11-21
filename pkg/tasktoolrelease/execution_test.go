package tasktoolrelease_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskExecutionSingleClaimAndRevocation(t *testing.T) {
	now := time.Now().UTC()
	record, err := tasktoolrelease.New(uuid.NewString(), uuid.NewString(), strings.Repeat("c", 64), fixture(), now)
	if err != nil {
		t.Fatal(err)
	}
	d := api.TaskToolReleaseIntentDecision{IntentSHA256: record.IntentSHA256, ReleaseFingerprint: record.Intent.ReleaseFingerprint, Decision: "approve"}
	record, err = tasktoolrelease.Decide(record, d, now)
	if err != nil {
		t.Fatal(err)
	}
	r := &api.TaskToolReleaseExecutionRequest{Action: "authorize", OperationID: strings.Repeat("d", 64), BindingsSHA256: strings.Repeat("e", 64)}
	d.Decision, d.AuthorizerKeyID, d.Execution = "authorize", uuid.NewString(), r
	if _, err = tasktoolrelease.Execution(record, d, false, now); err == nil {
		t.Fatal("unverified native authority authorized execution")
	}
	record, err = tasktoolrelease.Execution(record, d, true, now)
	if err != nil || record.Execution.State != "authorized" || record.ReadyToApply || record.BindingsVerified {
		t.Fatal(record, err)
	}
	initial := record
	r.Action, r.ClaimToken = "claim", strings.Repeat("f", 64)
	d.Decision = ""
	record, err = tasktoolrelease.Execution(record, d, true, now)
	if err != nil || record.Execution.State != "running" || initial.Execution.State != "authorized" {
		t.Fatal("claim or immutability failed", record, err)
	}
	claimed := record.Execution.ClaimSHA256
	r.ClaimToken = strings.Repeat("1", 64)
	duplicate, err := tasktoolrelease.Execution(record, d, true, now)
	if err != nil || duplicate.Execution.ClaimSHA256 != claimed {
		t.Fatal("duplicate obtained a replacement claim", duplicate, err)
	}
	r.Action = "guard"
	if _, err = tasktoolrelease.Execution(record, d, true, now); err == nil {
		t.Fatal("wrong claim guarded execution")
	}
	r.ClaimToken = strings.Repeat("f", 64)
	for _, changed := range []string{"expiry", "source", "binding", "native", "revoked"} {
		t.Run(changed, func(t *testing.T) {
			copy := record
			req := *r
			in := d
			in.Execution = &req
			when, fresh := now, true
			switch changed {
			case "expiry":
				when = record.ExpiresAt
			case "source":
				in.ReleaseFingerprint = strings.Repeat("2", 64)
			case "binding":
				req.BindingsSHA256 = strings.Repeat("2", 64)
			case "native":
				fresh = false
			case "revoked":
				copy.Status = "revoked"
			}
			out, e := tasktoolrelease.Execution(copy, in, fresh, when)
			if e == nil && out.Execution.State == "running" {
				t.Fatal("changed authority still permits running", out)
			}
		})
	}
	r.Action = "reconcile"
	uncertain, err := tasktoolrelease.Execution(record, d, false, record.ExpiresAt)
	if err != nil || uncertain.Execution.State != "reconcile_required" {
		t.Fatal(uncertain, err)
	}
	r.Action = "claim"
	if again, e := tasktoolrelease.Execution(uncertain, d, true, now); e != nil || again.Execution.State != "reconcile_required" {
		t.Fatal("uncertain work became retryable", again, e)
	}
	r.Action = "complete"
	done, err := tasktoolrelease.Execution(record, d, true, now)
	if err != nil || done.Execution.State != "completed" {
		t.Fatal(done, err)
	}
}
