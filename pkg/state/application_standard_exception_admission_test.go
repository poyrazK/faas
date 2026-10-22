package state_test

// Portable simulated grants verify storage authority only; they claim no
// physical byte consumption, consumer observation or native acceptance.
import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardExceptionRuntimeTestStore = state.ContractTestStore

func TestMemApplicationStandardExceptionNativeAdmission(t *testing.T) {
	standardExceptionNativeAdmission(t, state.NewMemStore())
}
func TestPgApplicationStandardExceptionNativeAdmission(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	standardExceptionNativeAdmission(t, s)
}

func standardRuntimeExceptionFixture(t *testing.T, s standardExceptionRuntimeTestStore, ttl time.Duration) (state.RuntimeCaptureFixture, state.ApplicationStandardException) {
	t.Helper()
	f := state.NewRuntimeCaptureFixture(t, s, true)
	ctx := t.Context()
	assignments, err := s.ListApplicationStandardAssignments(ctx, (*f.App()).OrgID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("fixture assignments: %+v %v", assignments, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, (*f.App()).OrgID, (*f.App()).ID)
	if err != nil {
		t.Fatal(err)
	}
	r := state.ApplicationStandardExceptionRequest{ExpectedRevision: e.DesiredRevision, StandardID: assignments[0].StandardID, Version: 1, Field: appstandards.EgressCIDRs, Value: json.RawMessage(`["1.1.1.0/24"]`), Reason: "Temporary partner network", ExpiresAt: time.Now().UTC().Add(ttl)}
	x, err := s.ApproveApplicationStandardException(ctx, (*f.App()).OrgID, (*f.App()).AccountID, (*f.App()).ID, r)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "runtime-exception-install")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, c); err != nil {
		t.Fatal(err)
	}
	(*f.App()), err = s.AppByID(ctx, (*f.App()).ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, x
}

func standardExceptionNativeAdmission(t *testing.T, s standardExceptionRuntimeTestStore) {
	t.Helper()
	f, x := standardRuntimeExceptionFixture(t, s, 2*time.Second)
	ctx := t.Context()
	ins, grant, receipt := state.NativeBootTestAttempt(t, s, f, state.StateColdBooting)
	capture, err := s.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
	if err != nil || !capture.ExceptionExpiresAt.Equal(x.ExpiresAt) || grant.ExpiresAtUnixNano != x.ExpiresAt.UnixNano() {
		t.Fatalf("capture/grant omitted expiry: %+v %+v %v", capture, grant, err)
	}
	if err := state.CheckInstanceApplicationStandardAdmission(ctx, s, ins.ID, (*f.App()), (*f.Owner()).Account, (*f.Dep())); err != nil {
		t.Fatal(err)
	}
	state.WaitStandardExceptionExpiry(t, x)
	if _, err := s.CreateInstance(ctx, (*f.App()).ID, (*f.Dep()).ID, string(state.StateWaking), 128, (*f.NodeID()), uuid.NewString()); !errors.Is(err, state.ErrApplicationStandardsPending) {
		t.Fatalf("expired new admission: %v", err)
	}
	if err := state.CheckInstanceApplicationStandardAdmission(ctx, s, ins.ID, (*f.App()), (*f.Owner()).Account, (*f.Dep())); !errors.Is(err, state.ErrApplicationStandardsPending) {
		t.Fatalf("expired captured admission: %v", err)
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(ctx, ins.State, grant); err == nil {
		t.Fatal("expired boot grant retried")
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, state.StateRunning, receipt); err == nil {
		t.Fatal("delayed receipt bypassed exception expiry")
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, (*f.App()).OrgID, (*f.App()).ID)
	if err != nil || e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatalf("refusal fabricated repair or observation: %+v %v", e, err)
	}
}

func TestPgApplicationStandardExceptionRawNativeDeadline(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	f, x := standardRuntimeExceptionFixture(t, s, time.Minute)
	ins, err := s.CreateInstance(t.Context(), (*f.App()).ID, (*f.Dep()).ID, string(state.StateColdBooting), 128, (*f.NodeID()), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	candidate := state.RuntimeCaptureTestBinding(t, s, ins)
	candidate.ExpiresAtUnixNano = x.ExpiresAt.UnixNano() + 1000
	raw, _ := json.Marshal(candidate)
	_, err = pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
	if !errors.Is(state.MapErr(err), state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw grant exceeded exception: %v", err)
	}
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != x.ExpiresAt.UnixNano() {
		t.Fatalf("legitimate bounded grant: %+v %v", grant, err)
	}
}
