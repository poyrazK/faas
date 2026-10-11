package neon

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.ComputePolicyProber = (*Provider)(nil)

func (p *Provider) ProbeComputePolicy(ctx context.Context, id string, spec managedpostgres.Spec) (evidence managedpostgres.ComputePolicyEvidence, err error) {
	key := "qualification-policy-" + uuid.NewString()
	fixture := "gregale_policy_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	target.ScaleToZero = !spec.ScaleToZero
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
	if err := p.probeComputePolicyPhase(ctx, id, target, writer, &evidence); err != nil {
		return evidence, err
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
	if err := p.probeComputePolicyPhase(ctx, id, spec, writer, &evidence); err != nil {
		return evidence, err
	}
	if verifyResizeMarker(ctx, writer, table, key, false) != nil || verifyResizeMarker(ctx, reader, table, key, true) != nil {
		return evidence, managedpostgres.ErrUnavailable
	}
	evidence.IdentityPreserved, evidence.DataPreserved, evidence.CredentialsPreserved, evidence.OriginalPolicyRestored = true, true, true, true
	return evidence, nil
}

func (p *Provider) probeComputePolicyPhase(ctx context.Context, id string, spec managedpostgres.Spec, writer managedpostgres.CredentialMaterial, evidence *managedpostgres.ComputePolicyEvidence) error {
	if spec.ScaleToZero {
		result, err := p.ProbeScaleToZero(ctx, id, writer)
		if err != nil {
			return err
		}
		if result.Validate() != nil {
			return managedpostgres.ErrUnavailable
		}
		evidence.Suspended, evidence.Resumed = true, true
		return nil
	}
	for {
		observed, err := p.Observe(ctx, id)
		if err != nil {
			return err
		}
		if observed.Spec != spec {
			return managedpostgres.ErrConflict
		}
		if observed.Status == managedpostgres.ProviderStatusReady && observed.ComputeState == managedpostgres.ComputeStateActive {
			evidence.AlwaysOnObserved = true
			return nil
		}
		timer := time.NewTimer(p.credentialPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
