package neon

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ComputeResizeProber = (*Provider)(nil)

func (p *Provider) ProbeComputeResize(ctx context.Context, id string, spec managedpostgres.Spec) (evidence managedpostgres.ResizeEvidence, err error) {
	key := "qualification-resize-" + uuid.NewString()
	fixture := "gregale_resize_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	writerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-writer", IdempotencyKey: key + "-writer", Access: managedpostgres.CredentialMigration}
	readerRequest := managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key + "-reader", IdempotencyKey: key + "-reader", Access: managedpostgres.CredentialReadOnly}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		owner, cleanupErr := p.qualificationOwnerConnection(cleanup, id)
		if cleanupErr == nil {
			_, cleanupErr = owner.Exec(cleanup, "DROP TABLE IF EXISTS public."+roleIdentifier(fixture))
			_ = owner.Close(cleanup)
		}
		for _, r := range []managedpostgres.CredentialRequest{readerRequest, writerRequest} {
			if revokeErr := p.RevokeCredentials(cleanup, r); revokeErr != nil {
				cleanupErr = revokeErr
			}
		}
		if cleanupErr != nil {
			err = managedpostgres.ErrUnavailable
		}
	}()
	observed, err := p.Inspect(ctx, id)
	if err != nil || observed.Status != managedpostgres.ProviderStatusReady || observed.Spec != spec || observed.DataResourceID == "" {
		return evidence, managedpostgres.ErrUnavailable
	}
	writer, err := p.IssueCredentials(ctx, writerRequest)
	if err != nil {
		return evidence, err
	}
	conn, err := connectProbe(ctx, writer)
	if err != nil {
		return evidence, err
	}
	table := "public." + roleIdentifier(fixture)
	_, err = conn.Exec(ctx, "CREATE TABLE "+table+" (value text PRIMARY KEY)")
	if err == nil {
		_, err = conn.Exec(ctx, "INSERT INTO "+table+" (value) VALUES ($1)", key)
	}
	_ = conn.Close(ctx)
	if err != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	reader, err := p.IssueCredentials(ctx, readerRequest)
	if err != nil {
		return evidence, err
	}
	target := spec
	if spec.Class == managedpostgres.ClassDevelopment {
		target.Class = managedpostgres.ClassBurstable
	} else {
		target.Class = managedpostgres.ClassDevelopment
	}
	request := managedpostgres.UpdateRequest{ResourceID: id, DataResourceID: observed.DataResourceID, PreviousSpec: spec, Spec: target, Generation: 2, IdempotencyKey: key}
	resized, err := p.waitForComputeResize(ctx, request)
	if err != nil {
		return evidence, err
	}
	replayed, err := p.Update(ctx, request)
	if err != nil || replayed.Status != managedpostgres.ProviderStatusReady || replayed.Spec != target || replayed.DataResourceID != resized.DataResourceID || replayed.ProviderResourceID != id {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.ReplayStable = true
	if verifyResizeMarker(ctx, writer, table, key, false) != nil || verifyResizeMarker(ctx, reader, table, key, true) != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	request.PreviousSpec, request.Spec, request.Generation, request.IdempotencyKey = target, spec, 3, key+"-restore"
	restored, err := p.waitForComputeResize(ctx, request)
	if err != nil {
		return evidence, err
	}
	if restored.DataResourceID != observed.DataResourceID || resized.DataResourceID != observed.DataResourceID || restored.ProviderResourceID != id || resized.ProviderResourceID != id {
		return evidence, managedpostgres.ErrUnavailable
	}
	if verifyResizeMarker(ctx, writer, table, key, false) != nil || verifyResizeMarker(ctx, reader, table, key, true) != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.IdentityPreserved, evidence.DataPreserved, evidence.CredentialsPreserved, evidence.OriginalClassRestored = true, true, true, true
	return evidence, nil
}

func (p *Provider) waitForComputeResize(ctx context.Context, request managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	for {
		observed, err := p.Update(ctx, request)
		if err != nil {
			return managedpostgres.ObservedDatabase{}, err
		}
		if observed.Status == managedpostgres.ProviderStatusReady && observed.Spec == request.Spec {
			return observed, nil
		}
		if observed.Status != managedpostgres.ProviderStatusPending {
			return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
		}
		timer := time.NewTimer(p.credentialPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return managedpostgres.ObservedDatabase{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func verifyResizeMarker(ctx context.Context, material managedpostgres.CredentialMaterial, table, marker string, readOnly bool) error {
	conn, err := connectProbe(ctx, material)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	var value, user string
	if err := conn.QueryRow(ctx, "SELECT value,session_user FROM "+table).Scan(&value, &user); err != nil || value != marker || user != material.Username {
		return managedpostgres.ErrUnavailable
	}
	if readOnly && !sqlPermissionDenied(ctx, conn, "UPDATE "+table+" SET value='forbidden'") {
		return managedpostgres.ErrUnavailable
	}
	return nil
}
