package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardAssignmentInventoryTestStore interface {
	standardOperationTestStore
	ApplicationStandardOperationControlStore
	ApplicationStandardAssignmentInventoryStore
}

func TestMemApplicationStandardAssignmentInventoryLifecycle(t *testing.T) {
	standardAssignmentInventoryLifecycle(t, NewMemStore())
}

func standardAssignmentInventoryLifecycle(t *testing.T, s standardAssignmentInventoryTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardApprovalFixture(t, s)
	org, actor := f.owner.PersonalOrg.ID, f.owner.Account.ID
	empty, err := s.ListApplicationStandardAssignmentRecords(ctx, org, "", api.ApplicationStandardMaxListPage)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty inventory: %+v %v", empty, err)
	}
	op, err := s.ApproveApplicationStandardReview(ctx, org, actor, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.GetApplicationStandardAssignmentRecord(ctx, strings.ToUpper(org), strings.ToUpper(strings.ReplaceAll(op.AssignmentID, "-", "")))
	if err != nil || first.Revision != 1 || !first.Active || first.AdmissionVersion != 1 || first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() || uuid.MustParse(first.CreatedBy) != uuid.MustParse(actor) {
		t.Fatalf("active record: %+v %v", first, err)
	}
	if _, err = s.ControlApplicationStandardOperation(ctx, org, actor, op.ID, op.UpdatedAt, ApplicationStandardOperationAbort); err != nil {
		t.Fatal(err)
	}
	r := f.plan.Request
	r.AssignmentID, r.ExpectedRevision, r.Active = first.ID, first.Revision, false
	p, err := s.PreviewApplicationStandardAssignment(ctx, org, actor, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveApplicationStandardReview(ctx, org, actor, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	inactive, err := s.GetApplicationStandardAssignmentRecord(ctx, org, first.ID)
	if err != nil || inactive.Active || inactive.Revision != 2 || inactive.ID != first.ID || inactive.CreatedBy != first.CreatedBy || !inactive.CreatedAt.Equal(first.CreatedAt) || inactive.UpdatedAt.Before(first.UpdatedAt) {
		t.Fatalf("retained record: %+v %v", inactive, err)
	}
	rows, err := s.ListApplicationStandardAssignmentRecords(ctx, org, "", 1)
	if err != nil || len(rows) != 1 || rows[0] != inactive {
		t.Fatalf("inactive record omitted: %+v %v", rows, err)
	}
	active, err := s.ListApplicationStandardAssignments(ctx, org)
	if err != nil || len(active) != 0 {
		t.Fatalf("inventory changed admission selection: %+v %v", active, err)
	}
	if _, err = s.GetApplicationStandardAssignmentRecord(ctx, uuid.NewString(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign assignment visible: %v", err)
	}
}

func TestMemApplicationStandardAssignmentInventoryPaging(t *testing.T) {
	m := NewMemStore()
	standardAssignmentInventoryPaging(t, m, func(a api.ApplicationStandardAssignment) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.applicationStandardAssignments == nil {
			m.applicationStandardAssignments = map[string]applicationStandardAssignmentRecord{}
		}
		m.applicationStandardAssignments[a.ID] = applicationStandardAssignmentRecord{Assignment: appstandards.Assignment{ID: a.ID, OrgID: a.OrgID, Scope: a.Scope, ScopeID: a.ScopeID, StandardID: a.StandardID, AdmissionVersion: a.AdmissionVersion}, Revision: a.Revision, Active: a.Active, CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	})
}

func standardAssignmentInventoryPaging(t *testing.T, s standardAssignmentInventoryTestStore, seed func(api.ApplicationStandardAssignment)) {
	t.Helper()
	ctx := t.Context()
	f := newStandardApprovalFixture(t, s)
	org, actor := f.owner.PersonalOrg.ID, f.owner.Account.ID
	count := api.ApplicationStandardMaxListPage + 2
	for i := count; i > 0; i-- {
		v, err := s.PublishApplicationStandardVersion(ctx, ApplicationStandardPublish{OrgID: org, ActorID: actor, Slug: fmt.Sprintf("inventory-%03d", i), CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"warn"}}`)}})
		if err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().UTC().Truncate(time.Microsecond)
		seed(api.ApplicationStandardAssignment{ID: fmt.Sprintf("00000000-0000-4000-8000-%012x", i), OrgID: org, Scope: "organization", ScopeID: org, StandardID: v.StandardID, AdmissionVersion: 1, Revision: int64(i), CreatedBy: actor, CreatedAt: stamp, UpdatedAt: stamp})
	}
	rows, err := s.ListApplicationStandardAssignmentRecords(ctx, org, "", api.ApplicationStandardMaxListPage+1)
	if err != nil || len(rows) != api.ApplicationStandardMaxListPage+1 {
		t.Fatalf("sentinel was truncated: len=%d %v", len(rows), err)
	}
	for i, a := range rows {
		if a.ID != fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1) || a.Active || a.Revision != int64(i+1) {
			t.Fatalf("unstable retained ordering: %+v", a)
		}
	}
	next, err := s.ListApplicationStandardAssignmentRecords(ctx, org, strings.ToUpper(strings.ReplaceAll(rows[len(rows)-1].ID, "-", "")), 1)
	if err != nil || len(next) != 1 || next[0].Revision != int64(count) {
		t.Fatalf("canonical cursor: %+v %v", next, err)
	}
	last, err := s.ListApplicationStandardAssignmentRecords(ctx, org, next[0].ID, 1)
	if err != nil || last == nil || len(last) != 0 {
		t.Fatalf("terminal page: %+v %v", last, err)
	}
	foreign, err := s.ListApplicationStandardAssignmentRecords(ctx, uuid.NewString(), "", 1)
	if err != nil || foreign == nil || len(foreign) != 0 {
		t.Fatalf("foreign inventory visible: %+v %v", foreign, err)
	}
}

func TestMemApplicationStandardAssignmentInventoryValidation(t *testing.T) {
	standardAssignmentInventoryValidation(t, NewMemStore())
}

func standardAssignmentInventoryValidation(t *testing.T, s ApplicationStandardAssignmentInventoryStore) {
	t.Helper()
	org, id := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		org, after string
		limit      int
	}{{"", "", 1}, {"bad", "", 1}, {org, "bad", 1}, {org, uuid.Nil.String(), 1}, {org, "", 0}, {org, "", api.ApplicationStandardMaxListPage + 2}} {
		if _, err := s.ListApplicationStandardAssignmentRecords(t.Context(), tc.org, tc.after, tc.limit); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid page accepted: %+v %v", tc, err)
		}
	}
	for _, invalid := range []string{"", "bad", uuid.Nil.String()} {
		if _, err := s.GetApplicationStandardAssignmentRecord(t.Context(), org, invalid); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid ID accepted: %q %v", invalid, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ListApplicationStandardAssignmentRecords(ctx, org, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if _, err := s.GetApplicationStandardAssignmentRecord(ctx, org, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
}
