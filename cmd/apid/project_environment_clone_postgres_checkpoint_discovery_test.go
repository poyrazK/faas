//go:build !no_pg

// adr: 590
package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/state"
)

func (p *cloneCheckpointClosureProvider) DiscoverCheckpointConnections(ctx context.Context, d managedpostgres.RestoreSourceDefinition, m managedpostgres.CheckpointMaintenance, identity managedpostgres.CheckpointConnectionIdentity) (managedpostgres.CheckpointConnectionRequest, error) {
	p.discoveries++
	if _, ok := ctx.Deadline(); !ok {
		p.deadlineMissing = true
	}
	if m != p.maintenance || identity.SourceResourceID != d.DataResourceID {
		return managedpostgres.CheckpointConnectionRequest{}, managedpostgres.ErrConflict
	}
	if p.onDiscover != nil {
		if err := p.onDiscover(ctx); err != nil {
			return managedpostgres.CheckpointConnectionRequest{}, err
		}
	}
	names := slices.Clone(p.inventory)
	if names == nil {
		names = []string{"source_private_alpha", "source_private_beta"}
	}
	slices.Sort(names)
	return managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: identity, DatabaseNames: names}, nil
}

func TestPGClonePostgresCheckpointDiscoveryRetainsOriginalAcrossLostReplyAndHandoff(t *testing.T) {
	f, store, provider, plan, previous, _, _ := cloneCheckpointSelectionWorkerFixture(t)
	store.loseRecord = true
	actual, err := f.srv.discoverProjectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(actual, checkpointselection.Selection{}) || provider.discoveries != 1 || provider.deadlineMissing {
		t.Fatalf("lost committed discovery: calls=%d err=%v", provider.discoveries, err)
	}
	original, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(t.Context(), f.lease, plan.source.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, previous} }
	provider.inventory = []string{"replacement_catalogue"}
	if _, err := f.srv.discoverProjectEnvironmentClonePostgresCheckpointSelection(t.Context(), stale, plan); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale discovery recovered: %v", err)
	}
	wrong := plan
	wrong.hash = "wrong frozen source"
	if _, err := f.srv.discoverProjectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong frozen plan recovered: %v", err)
	}
	// Recovery needs the original decryption key, but neither live discovery nor
	// a configured provider. The committed encrypted selection remains exact.
	f.srv.managedPostgres = nil
	selection, err := f.srv.discoverProjectEnvironmentClonePostgresCheckpointSelection(t.Context(), f.lease, plan)
	if err != nil || provider.discoveries != 1 {
		t.Fatalf("handoff rediscovered source: calls=%d err=%v", provider.discoveries, err)
	}
	request, err := selection.RequestForWorker(original.Sealed.Scope)
	if err != nil || request.OwnerToken != f.lease.Operation.ID || !slices.Equal(request.DatabaseNames, []string{"source_private_alpha", "source_private_beta"}) {
		t.Fatalf("original native intent changed: %v", err)
	}
	recovered, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(t.Context(), f.lease, plan.source.ID)
	if err != nil || !bytes.Equal(original.Sealed.Ciphertext, recovered.Sealed.Ciphertext) || original.Sealed.Fingerprint != recovered.Sealed.Fingerprint || !original.RetainedAt.Equal(recovered.RetainedAt) {
		t.Fatalf("handoff replaced original receipt: %v", err)
	}
	assertCloneCheckpointHold(t, f, store)
}

func TestPGClonePostgresCheckpointDiscoveryFencesAdmissionAndPostReadDrift(t *testing.T) {
	for _, fault := range []string{"admission", "cancel", "handoff", "placement"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, plan, _, _, _ := cloneCheckpointSelectionWorkerFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if fault == "admission" {
				f.srv.cloneWorkerAdmission = func(context.Context) error { return managedpostgres.ErrUnavailable }
			} else {
				provider.onDiscover = func(readCtx context.Context) error {
					switch fault {
					case "cancel":
						cancel()
					case "handoff":
						if err := store.ReleaseProjectEnvironmentCloneLease(readCtx, f.lease, 0); err != nil {
							return err
						}
						var err error
						f.lease, err = store.ClaimNextProjectEnvironmentClone(readCtx, uuid.NewString(), time.Minute)
						return err
					case "placement":
						_, err := f.pool.Exec(readCtx, "update managed_postgres_databases set postgres_major=postgres_major+1 where id=$1", plan.source.ID)
						return err
					}
					return nil
				}
			}
			lease := f.lease
			actual, err := f.srv.discoverProjectEnvironmentClonePostgresCheckpointSelection(ctx, lease, plan)
			if err == nil || !reflect.DeepEqual(actual, checkpointselection.Selection{}) {
				t.Fatalf("%s returned usable selection: %v", fault, err)
			}
			if fault == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if fault == "admission" && provider.discoveries != 0 || fault != "admission" && provider.discoveries != 1 {
				t.Fatalf("unexpected discovery dispatch: %d", provider.discoveries)
			}
			var retained int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_postgres_checkpoint_selections where operation_id=$1", f.lease.Operation.ID).Scan(&retained); err != nil || retained != 0 {
				t.Fatalf("failed discovery retained intent: %d %v", retained, err)
			}
			assertCloneCheckpointHold(t, f, store)
		})
	}
}
