package state

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardExceptionTestStore interface {
	standardLocalIntentTestStore
	ApplicationStandardExceptionStore
}

func standardExceptionRequest(f standardLocalIntentFixture) ApplicationStandardExceptionRequest {
	return ApplicationStandardExceptionRequest{ExpectedRevision: f.enrollment.DesiredRevision, StandardID: f.version.StandardID, Version: 1, Field: appstandards.LogDestinations, Value: mustStandardLocalJSON([]string{f.rotated.ID}), Reason: "Temporary collector maintenance", ExpiresAt: time.Now().UTC().Add(time.Hour)}
}

func TestMemApplicationStandardExceptionLifecycle(t *testing.T) {
	standardExceptionLifecycle(t, NewMemStore())
}

func standardExceptionLifecycle(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := standardExceptionRequest(f)
	x, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil || !sameStandardUUID(x.ApprovedBy, f.owner.Account.ID) || x.CreatedAt.IsZero() || x.RevokedAt != nil {
		t.Fatalf("approval: %+v %v", x, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, x.OrgID, x.AppID)
	if err != nil || e.State != "pending" || e.DesiredRevision != r.ExpectedRevision+1 || e.PersistedRevision != r.ExpectedRevision || e.ObservedRevision != 0 || e.ExceptionExpiresAt != nil {
		t.Fatalf("queued exception: %+v %v", e, err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if f.enrollment.ExceptionExpiresAt == nil || !f.enrollment.ExceptionExpiresAt.Equal(x.ExpiresAt) || f.enrollment.Effective.Sources[r.Field][0].ExceptionID != x.ID {
		t.Fatalf("exception projection: %+v", f.enrollment)
	}
	assertStandardExceptionDrains(t, s, f.app.ID, "https://rotated.example.com/logs")
	if _, err := s.ApproveApplicationStandardException(ctx, x.OrgID, f.owner.Account.ID, x.AppID, standardExceptionRequest(f)); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap accepted: %v", err)
	}
	x, err = s.RevokeApplicationStandardException(ctx, x.OrgID, f.owner.Account.ID, x.AppID, x.ID, f.enrollment.DesiredRevision)
	if err != nil || x.RevokedAt == nil || !sameStandardUUID(x.RevokedBy, f.owner.Account.ID) {
		t.Fatalf("revocation: %+v %v", x, err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if f.enrollment.ExceptionExpiresAt != nil || f.enrollment.Effective.Sources[r.Field][0].ExceptionID != "" {
		t.Fatalf("revoked projection: %+v", f.enrollment)
	}
	assertStandardExceptionDrains(t, s, f.app.ID, "https://company.example.com/logs")
	history, err := s.ListApplicationStandardExceptions(ctx, x.OrgID, x.AppID, "")
	if err != nil || len(history) != 1 || history[0].RevokedAt == nil || history[0].Reason != r.Reason {
		t.Fatalf("retained exception: %+v %v", history, err)
	}
	if _, err := s.RevokeApplicationStandardException(ctx, x.OrgID, f.owner.Account.ID, x.AppID, x.ID, f.enrollment.DesiredRevision); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated revoke: %v", err)
	}
	logs, err := s.ListAuditLog(ctx, AuditLogFilter{KindPrefix: "application_standard.exception_", IncludeAnonymous: true, Limit: 100})
	if err != nil || len(logs) != 2 {
		t.Fatalf("exception audit: %+v %v", logs, err)
	}
	for _, a := range logs {
		if strings.Contains(string(a.Data), r.Reason) || strings.Contains(string(a.Data), "example.com") {
			t.Fatal("audit disclosed configuration or reason")
		}
	}
}

func TestMemApplicationStandardExceptionRefusals(t *testing.T) {
	standardExceptionRefusals(t, NewMemStore())
}

func standardExceptionRefusals(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	for _, tc := range []struct {
		name   string
		change func(*ApplicationStandardExceptionRequest)
	}{
		{"stale", func(r *ApplicationStandardExceptionRequest) { r.ExpectedRevision++ }},
		{"unadopted version", func(r *ApplicationStandardExceptionRequest) { r.Version = 2 }},
		{"foreign standard", func(r *ApplicationStandardExceptionRequest) { r.StandardID = uuid.NewString() }},
		{"unknown field", func(r *ApplicationStandardExceptionRequest) { r.Field = "custom" }},
		{"null", func(r *ApplicationStandardExceptionRequest) { r.Value = json.RawMessage(`null`) }},
		{"duplicate", func(r *ApplicationStandardExceptionRequest) { r.Value = json.RawMessage(`["a","a"]`) }},
		{"foreign resource", func(r *ApplicationStandardExceptionRequest) {
			r.Value = mustStandardLocalJSON([]string{uuid.NewString()})
		}},
		{"no reason", func(r *ApplicationStandardExceptionRequest) { r.Reason = " \n " }},
		{"large reason", func(r *ApplicationStandardExceptionRequest) {
			r.Reason = strings.Repeat("x", api.ApplicationStandardMaxDescriptionBytes+1)
		}},
		{"invalid utf8", func(r *ApplicationStandardExceptionRequest) { r.Reason = string([]byte{255}) }},
		{"expired", func(r *ApplicationStandardExceptionRequest) { r.ExpiresAt = time.Now().Add(-time.Second) }},
		{"long ttl", func(r *ApplicationStandardExceptionRequest) {
			r.ExpiresAt = time.Now().Add(api.ApplicationStandardMaxExceptionTTL + time.Hour)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := standardExceptionRequest(f)
			tc.change(&r)
			if _, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); err == nil {
				t.Fatal("invalid approval accepted")
			}
		})
	}
	got, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || got.DesiredRevision != f.enrollment.DesiredRevision || got.State != "persisted" {
		t.Fatalf("refusals changed enrollment: %+v %v", got, err)
	}
	history, err := s.ListApplicationStandardExceptions(ctx, f.owner.PersonalOrg.ID, f.app.ID, "")
	if err != nil || len(history) != 0 {
		t.Fatalf("refusals left approvals: %+v %v", history, err)
	}
}

func TestMemApplicationStandardExceptionConcurrent(t *testing.T) {
	standardExceptionConcurrent(t, NewMemStore())
}

func standardExceptionConcurrent(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := standardExceptionRequest(f)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrApplicationStandardLocalIntentStale) {
			t.Fatalf("concurrent refusal: %v", err)
		}
	}
	history, err := s.ListApplicationStandardExceptions(ctx, f.owner.PersonalOrg.ID, f.app.ID, "")
	if success != 1 || err != nil || len(history) != 1 {
		t.Fatalf("concurrent approval: %d %+v %v", success, history, err)
	}
}

func TestMemApplicationStandardExceptionExpiry(t *testing.T) {
	standardExceptionExpiry(t, NewMemStore())
}

func standardExceptionExpiry(t *testing.T, s standardExceptionTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := standardExceptionRequest(f)
	r.ExpiresAt = time.Now().UTC().Add(2 * time.Second)
	x, err := s.ApproveApplicationStandardException(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if !applicationStandardEnrollmentPermitsRuntimeAt(f.app, f.enrollment, x.ExpiresAt.Add(-time.Microsecond)) || applicationStandardEnrollmentPermitsRuntimeAt(f.app, f.enrollment, x.ExpiresAt) {
		t.Fatal("deadline not exact")
	}
	timer := time.NewTimer(time.Until(x.ExpiresAt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	if ApplicationStandardEnrollmentPermitsRuntime(f.app, f.enrollment) {
		t.Fatal("stalled repair extended exception")
	}
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "exception-expiry-worker")
	if err != nil || c.DesiredRevision != f.enrollment.DesiredRevision+1 {
		t.Fatalf("expiry queue: %+v %v", c, err)
	}
	pending, err := s.GetApplicationStandardEnrollment(ctx, x.OrgID, x.AppID)
	if err != nil || pending.PersistedRevision != f.enrollment.PersistedRevision || pending.ObservedRevision != 0 {
		t.Fatalf("expiry invented installation: %+v %v", pending, err)
	}
	e, err := s.MaterializeApplicationStandardEnrollment(ctx, c)
	if err != nil || e.ExceptionExpiresAt != nil || e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatalf("expiry repair: %+v %v", e, err)
	}
	history, err := s.ListApplicationStandardExceptions(ctx, x.OrgID, x.AppID, "")
	if err != nil || len(history) != 1 || history[0].RevokedAt != nil {
		t.Fatalf("expired history: %+v %v", history, err)
	}
	logs, err := s.ListAuditLog(ctx, AuditLogFilter{KindPrefix: "application_standard.exception_expiry_queued", IncludeAnonymous: true, Limit: 100})
	if err != nil || len(logs) != 1 {
		t.Fatalf("expiry audit: %+v %v", logs, err)
	}
	if _, err := s.ClaimApplicationStandardEnrollment(ctx, "exception-expiry-retry"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expiry queued twice: %v", err)
	}
}

func TestApplicationStandardExceptionNativeDeadline(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	deadline := now.Add(time.Minute)
	raw := mustStandardLocalJSON(map[string]any{"app_id": uuid.NewString(), "settings": map[string]any{"require_signed": false, "security_policy": "off"}, "exception_expires_at_unix_nano": deadline.UnixNano()})
	c, err := decodeInstanceStandardAdmission(uuid.NewString(), raw, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := standardNativeArtifactDeadline(c, DeploymentRuntimeScanEvidence{})
	if err != nil || !got.Equal(deadline) {
		t.Fatalf("native exception deadline: %v %v", got, err)
	}
	expires, err := standardNativeGrantExpiry(now, got)
	if err != nil || !expires.Equal(deadline) || standardNativeGrantWithinArtifactLease(deadline.Add(time.Microsecond).UnixNano(), got) {
		t.Fatalf("grant exceeded deadline: %v %v", expires, err)
	}
	if _, err := standardNativeGrantExpiry(deadline, got); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("expired native authority: %v", err)
	}
}

func assertStandardExceptionDrains(t *testing.T, s standardExceptionTestStore, appID, target string) {
	t.Helper()
	drains, err := s.ListAppLogDrainsForApp(t.Context(), appID)
	if err != nil || len(drains) != 1 || drains[0].TargetURL != target {
		t.Fatalf("exception destination: %+v %v", drains, err)
	}
}
