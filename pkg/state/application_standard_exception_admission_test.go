package state

// Portable simulated grants verify storage authority only; they claim no
// physical byte consumption, consumer observation or native acceptance.
import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardExceptionRuntimeTestStore interface {
	standardRuntimeCaptureTestStore
	ApplicationStandardExceptionStore
}

func TestMemApplicationStandardExceptionNativeAdmission(t *testing.T) {
	standardExceptionNativeAdmission(t, NewMemStore())
}
func TestPgApplicationStandardExceptionNativeAdmission(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardExceptionNativeAdmission(t, s)
}

func standardRuntimeExceptionFixture(t *testing.T, s standardExceptionRuntimeTestStore, ttl time.Duration) (runtimeCaptureFixture, ApplicationStandardException) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ctx := t.Context()
	assignments, err := s.ListApplicationStandardAssignments(ctx, f.app.OrgID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("fixture assignments: %+v %v", assignments, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.app.OrgID, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := ApplicationStandardExceptionRequest{ExpectedRevision: e.DesiredRevision, StandardID: assignments[0].StandardID, Version: 1, Field: appstandards.EgressCIDRs, Value: json.RawMessage(`["1.1.1.0/24"]`), Reason: "Temporary partner network", ExpiresAt: time.Now().UTC().Add(ttl)}
	x, err := s.ApproveApplicationStandardException(ctx, f.app.OrgID, f.app.AccountID, f.app.ID, r)
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
	f.app, err = s.AppByID(ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, x
}

func standardExceptionNativeAdmission(t *testing.T, s standardExceptionRuntimeTestStore) {
	t.Helper()
	f, x := standardRuntimeExceptionFixture(t, s, 2*time.Second)
	ctx := t.Context()
	ins, grant, receipt := nativeBootTestAttempt(t, s, f, StateColdBooting)
	capture, err := s.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
	if err != nil || !capture.ExceptionExpiresAt.Equal(x.ExpiresAt) || grant.ExpiresAtUnixNano != x.ExpiresAt.UnixNano() {
		t.Fatalf("capture/grant omitted expiry: %+v %+v %v", capture, grant, err)
	}
	if err := CheckInstanceApplicationStandardAdmission(ctx, s, ins.ID, f.app, f.owner.Account, f.dep); err != nil {
		t.Fatal(err)
	}
	waitStandardExceptionExpiry(t, x)
	if _, err := s.CreateInstance(ctx, f.app.ID, f.dep.ID, string(StateWaking), 128, f.nodeID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("expired new admission: %v", err)
	}
	if err := CheckInstanceApplicationStandardAdmission(ctx, s, ins.ID, f.app, f.owner.Account, f.dep); !errors.Is(err, ErrApplicationStandardsPending) {
		t.Fatalf("expired captured admission: %v", err)
	}
	if _, err := s.IssueInstanceApplicationStandardBoot(ctx, ins.State, grant); err == nil {
		t.Fatal("expired boot grant retried")
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, StateRunning, receipt); err == nil {
		t.Fatal("delayed receipt bypassed exception expiry")
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.app.OrgID, f.app.ID)
	if err != nil || e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatalf("refusal fabricated repair or observation: %+v %v", e, err)
	}
}

func TestPgApplicationStandardExceptionRawNativeDeadline(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f, x := standardRuntimeExceptionFixture(t, s, time.Minute)
	ins, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(StateColdBooting), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	candidate := runtimeCaptureTestBinding(t, s, ins)
	candidate.ExpiresAtUnixNano = x.ExpiresAt.UnixNano() + 1000
	raw, _ := json.Marshal(candidate)
	_, err = pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
	if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw grant exceeded exception: %v", err)
	}
	grant, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, candidate)
	if err != nil || grant.ExpiresAtUnixNano != x.ExpiresAt.UnixNano() {
		t.Fatalf("legitimate bounded grant: %+v %v", grant, err)
	}
}
