// adr: 20261018000200
package rehearsalfault

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func receipt() Receipt {
	return Receipt{ID: uuid.NewString(), AccountID: uuid.NewString(), RunID: strings.Repeat("a", 32), AppID: uuid.NewString(), ResourceID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), InvocationID: uuid.NewString(), CheckpointAttemptID: uuid.NewString(), OwnerID: uuid.NewString(), BackendSHA256: strings.Repeat("b", 64), PlanSHA256: strings.Repeat("c", 64), Spec: Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 100, RecoverWithinMS: 5000}, State: "recovered", Activated: true, Recovered: true, OutageMS: 100}
}
func TestPostgresFaultRequiresIndependentRecoveryProof(t *testing.T) {
	if err := receipt().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Receipt){
		"unactivated": func(r *Receipt) { r.Activated = false }, "unrecovered": func(r *Receipt) { r.Recovered = false },
		"lost owner": func(r *Receipt) { r.OwnerID = "" }, "wrong phase": func(r *Receipt) { r.State = "active" },
		"short outage": func(r *Receipt) { r.OutageMS = 99 }, "failed recovery": func(r *Receipt) { r.Error = "owner lost" },
		"missing checkpoint": func(r *Receipt) { r.CheckpointAttemptID = "" }, "unbounded fault": func(r *Receipt) { r.Spec.UnavailableMS = 30001 },
	} {
		t.Run(name, func(t *testing.T) {
			r := receipt()
			change(&r)
			if r.Validate() == nil {
				t.Fatal("incomplete recovery passed")
			}
		})
	}
}
func TestPostgresFaultTransitionPinsItsScopeAndCannotEraseFailure(t *testing.T) {
	r := Row{Receipt: receipt(), CreatedAt: time.Now(), DeadlineAt: time.Now().Add(time.Minute)}
	r.State = "requested"
	r.Activated = false
	r.Recovered = false
	n := r
	n.State = "cutting"
	if !Transition(r, n) {
		t.Fatal("claim refused")
	}
	for _, mutate := range []func(*Row){func(r *Row) { r.ResourceID = uuid.NewString() }, func(r *Row) { r.OwnerID = uuid.NewString() }, func(r *Row) { r.Spec.UnavailableMS++ }, func(r *Row) { r.PlanSHA256 = strings.Repeat("f", 64) }} {
		changed := n
		mutate(&changed)
		if Transition(r, changed) {
			t.Fatal("fault identity changed")
		}
	}
	active := r
	active.State, active.Activated, active.RestoreAt = "active", true, time.Now()
	changedClock := active
	changedClock.State = "recovering"
	changedClock.RestoreAt = active.RestoreAt.Add(time.Second)
	if Transition(active, changedClock) {
		t.Fatal("outage clock changed after activation")
	}
	n = r
	n.State = "recovering"
	n.Error = "collector lost"
	if !Transition(r, n) {
		t.Fatal("cleanup refused")
	}
	f := n
	f.State = "recovered"
	f.Error = ""
	f.Activated = true
	f.Recovered = true
	if Transition(n, f) {
		t.Fatal("cleanup was adopted into passing evidence")
	}
}
