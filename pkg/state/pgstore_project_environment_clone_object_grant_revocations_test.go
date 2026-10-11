//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneGrantObservation(p grantrevocation.Plan, revoked bool, writers int64) grantrevocation.Observation {
	hash, _ := p.SHA256()
	return grantrevocation.Observation{Scope: p.Scope, RequestID: p.RequestID, PlanSHA256: hash,
		RevocationID: "provider-owned-retirement", AllNativeGrantsRevoked: revoked, InFlightWrites: writers}
}

func TestPgCloneObjectGrantRevocationOriginalSelectionAndRecovery(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	l, buckets, foreign := cloneObjectWriteFenceFixture(t, s)
	b := buckets[0]
	request, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for range 2 {
		g, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, g.ID)
	}
	if _, err := s.BeginObjectBucketMutation(ctx, foreign, state.ObjectBucketMutationNativeGrant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID); err == nil {
		t.Fatal("retained selection without an owned admission hold")
	}
	if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
	slices.Sort(ids)
	if err != nil || !slices.Equal(r.Plan.GrantIDs, ids) || r.State != "reserved" || !r.RequestStartedAt.IsZero() {
		t.Fatalf("original selection: %+v %v", r, err)
	}
	original := r.Plan.Clone()
	r.Plan.GrantIDs[0] = uuid.NewString()
	r, err = s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
	if err != nil || !reflect.DeepEqual(r.Plan, original) {
		t.Fatalf("retry replaced original intent: %+v %v", r, err)
	}
	if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, original, cloneGrantObservation(original, true, 0)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retired undispatched grants: %v", err)
	}
	r, err = s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, original)
	if err != nil || r.RequestStartedAt.IsZero() || r.State != "dispatched" {
		t.Fatalf("dispatch: %+v %v", r, err)
	}
	started := r.RequestStartedAt
	for _, observation := range []grantrevocation.Observation{cloneGrantObservation(original, false, 0), cloneGrantObservation(original, true, 1)} {
		if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, original, observation); err != nil {
			t.Fatal(err)
		}
		fences, err := s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, l)
		if err != nil || len(fences) != 1 || fences[0].NativeGrants != 2 || fences[0].Requests != 1 {
			t.Fatalf("partial observation cleared writers: %+v %v", fences, err)
		}
	}
	stale := l
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, l, 0); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, stale, original, cloneGrantObservation(original, true, 0)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker retired grants: %v", err)
	}
	r, err = s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, original)
	if err != nil || !reflect.DeepEqual(r.Plan, original) || !r.RequestStartedAt.Equal(started) {
		t.Fatalf("handoff changed dispatch identity: %+v %v", r, err)
	}
	for range 2 {
		r, err = s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, original, cloneGrantObservation(original, true, 0))
		if err != nil || r.State != "drained" || r.DrainedAt.IsZero() {
			t.Fatalf("drain replay: %+v %v", r, err)
		}
	}
	fences, err := s.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, l)
	if err != nil || fences[0].NativeGrants != 0 || fences[0].Requests != 1 {
		t.Fatalf("drain touched independent request: %+v %v", fences, err)
	}
	if err := s.FinishObjectBucketMutation(ctx, request); err != nil {
		t.Fatal(err)
	}
	f, err := s.AcquireObjectBucketWriteFence(ctx, foreign, uuid.NewString())
	if err != nil || f.NativeGrants != 1 {
		t.Fatalf("drain changed another source: %+v %v", f, err)
	}
}

func TestPgCloneObjectGrantRevocationRejectsSubstitution(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	l, buckets, _ := cloneObjectWriteFenceFixture(t, s)
	b := buckets[0]
	for range 2 {
		if _, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "request", "hash", "negative", "missing_identity", "omit", "duplicate", "request_replacement", "revision", "token"} {
		t.Run(fault, func(t *testing.T) {
			p, badLease := r.Plan.Clone(), l
			o := cloneGrantObservation(p, true, 0)
			switch fault {
			case "scope":
				o.Scope.PhysicalName += "-replacement"
			case "request":
				o.RequestID = uuid.NewString()
			case "hash":
				o.PlanSHA256 = "other"
			case "negative":
				o.InFlightWrites = -1
			case "missing_identity":
				o.RevocationID = ""
			case "omit":
				p.GrantIDs = p.GrantIDs[:1]
				o = cloneGrantObservation(p, true, 0)
			case "duplicate":
				p.GrantIDs[1] = p.GrantIDs[0]
				o = cloneGrantObservation(p, true, 0)
			case "request_replacement":
				p.RequestID = uuid.NewString()
				o = cloneGrantObservation(p, true, 0)
			case "revision":
				badLease.Operation.Revision--
			case "token":
				badLease.Token = uuid.NewString()
			}
			if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, badLease, p, o); err == nil {
				t.Fatal("substituted observation retired grants")
			}
		})
	}
	if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, cloneGrantObservation(r.Plan, true, 1)); err != nil {
		t.Fatal(err)
	}
	o := cloneGrantObservation(r.Plan, true, 0)
	o.RevocationID += "-replacement"
	if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, o); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed provider retirement identity: %v", err)
	}
	if _, err := pool.Exec(ctx, "update object_bucket_mutations set physical_name=physical_name||'-drift' where id=$1", r.Plan.GrantIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, cloneGrantObservation(r.Plan, true, 0)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cleared substituted original grant: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "select count(*) from object_bucket_mutations where bucket_id=$1", b.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rejections consumed grants: %d %v", count, err)
	}
}

func TestPgCloneObjectGrantRevocationAbandonmentRequiresDrain(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		t.Run(map[bool]string{false: "undispatched", true: "unknown_dispatch"}[dispatched], func(t *testing.T) {
			s, ctx, _ := pgWithPool(t)
			l, buckets, _ := cloneObjectWriteFenceFixture(t, s)
			b := buckets[0]
			if _, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
				t.Fatal(err)
			}
			r, err := s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if dispatched {
				if _, err := s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan); err != nil {
					t.Fatal(err)
				}
			}
			op := l.Operation
			l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if dispatched {
				if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, l); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("unknown native outcome reopened source: %v", err)
				}
				if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, cloneGrantObservation(r.Plan, true, 0)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("compensation started native revocation: %v", err)
			}
			if err := s.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, l); err != nil {
				t.Fatal(err)
			}
			f, err := s.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
			want := int64(1)
			if dispatched {
				want = 0
			}
			if err != nil || f.NativeGrants != want {
				t.Fatalf("abandonment cleared unknown grant: %+v %v", f, err)
			}
		})
	}
}

func TestPgCloneObjectGrantRevocationRejectsRosterDrift(t *testing.T) {
	for _, fault := range []string{"extra", "missing"} {
		t.Run(fault, func(t *testing.T) {
			s, ctx, pool := pgWithPool(t)
			l, buckets, _ := cloneObjectWriteFenceFixture(t, s)
			b := buckets[0]
			g, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
				t.Fatal(err)
			}
			r, err := s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan); err != nil {
				t.Fatal(err)
			}
			if fault == "extra" {
				_, err = pool.Exec(ctx, "insert into object_bucket_mutations(id,bucket_id,kind,backend_id,backend_fingerprint,physical_name) values($1,$2,'native_grant',$3,$4,$5)", uuid.NewString(), b.ID, b.BackendID, b.BackendFingerprint, b.PhysicalName)
			} else {
				_, err = pool.Exec(ctx, "delete from object_bucket_mutations where id=$1", g.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, cloneGrantObservation(r.Plan, true, 0)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("provider observation accepted %s roster: %v", fault, err)
			}
			var status string
			if err := pool.QueryRow(ctx, "select state from project_environment_clone_object_grant_revocations where operation_id=$1 and source_bucket_id=$2", l.Operation.ID, b.ID).Scan(&status); err != nil || status != "dispatched" {
				t.Fatalf("roster drift completed retirement: %s %v", status, err)
			}
		})
	}
}

func TestPgCloneObjectGrantRevocationLeaseExpiryAfterSourceLock(t *testing.T) {
	for _, step := range []string{"reserve", "read", "dispatch", "observe"} {
		t.Run(step, func(t *testing.T) {
			s, ctx, pool := pgWithPool(t)
			l, buckets, _ := cloneObjectWriteFenceFixture(t, s)
			b := buckets[0]
			if _, err := s.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationNativeGrant); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, b.ID); err != nil {
				t.Fatal(err)
			}
			r, err := s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if step == "observe" {
				if _, err := s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan); err != nil {
					t.Fatal(err)
				}
			}
			if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1 returning lease_until", l.Operation.ID).Scan(&l.ExpiresAt); err != nil {
				t.Fatal(err)
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lock.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := lock.Exec(ctx, "select id from object_buckets where id=$1 for update", b.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch step {
				case "reserve":
					_, err = s.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, b.ID)
				case "read":
					_, err = s.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, l, b.ID)
				case "dispatch":
					_, err = s.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan)
				case "observe":
					_, err = s.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, r.Plan, cloneGrantObservation(r.Plan, true, 0))
				}
				done <- err
			}()
			waitCloneObjectWriteFenceLock(t, ctx, pool)
			time.Sleep(max(0, time.Until(l.ExpiresAt)) + 30*time.Millisecond)
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired owner %s: %v", step, err)
			}
			var grants int
			if err := pool.QueryRow(ctx, "select count(*) from object_bucket_mutations where bucket_id=$1", b.ID).Scan(&grants); err != nil || grants != 1 {
				t.Fatalf("expired worker consumed grants: %d %v", grants, err)
			}
		})
	}
}
