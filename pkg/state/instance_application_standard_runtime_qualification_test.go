package state

// Durable-store tests use simulated native consumption. They do not prove KVM
// enforcement or whole-application convergence.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardRuntimeQualificationTestStore interface {
	nativeArtifactTestStore
	InstanceApplicationStandardRuntimeQualificationStore
	InstanceApplicationStandardRuntimeReceiptStore
	SetComputeNodeActive(context.Context, string, bool) error
}

func standardRuntimeQualificationRead(t *testing.T, s standardRuntimeQualificationTestStore, ins Instance, reason string) InstanceApplicationStandardRuntimeQualification {
	t.Helper()
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.GetInstanceApplicationStandardRuntimeQualification(t.Context(), app.OrgID, app.ID, ins.ID)
	if err != nil || q.Reason != reason || q.Qualified != (reason == "") || !sameStandardUUID(q.InstanceID, ins.ID) || q.CheckedAt.IsZero() || q.CheckedAt.After(time.Now()) || !runtimeadmission.ValidHash(q.RosterFingerprint) {
		t.Fatalf("qualification: %+v %v; want reason %s", q, err, reason)
	}
	if q.Qualified && !q.ApprovalExpiresAt.After(q.CheckedAt) || !q.Qualified && !q.ApprovalExpiresAt.IsZero() {
		t.Fatal("qualification invented an approval deadline")
	}
	raw, err := json.Marshal(q)
	if err != nil || strings.Contains(string(raw), "native-artifact") || strings.Contains(string(raw), "10.100.") || strings.Contains(string(raw), "storage_key") || strings.Contains(string(raw), "sealed") {
		t.Fatal("qualification exposed native resources or private configuration")
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.ObservedRevision != 0 || e.State != "persisted" {
		t.Fatal("instance qualification advanced application observation", err)
	}
	return q
}

func standardRuntimeQualificationLifecycle(t *testing.T, s standardRuntimeQualificationTestStore) {
	t.Helper()
	ctx := t.Context()
	ins, receipt := issueConsumedNativeFixture(t, s)
	standardRuntimeQualificationRead(t, s, ins, "runtime_transition_pending")
	ins, err := s.PublishInstanceApplicationStandardRuntime(ctx, ins.State, StateRunning, receipt)
	if err != nil {
		t.Fatal(err)
	}
	q := standardRuntimeQualificationRead(t, s, ins, "")
	app, err := s.AppByID(ctx, ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][3]string{{uuid.NewString(), app.ID, ins.ID}, {app.OrgID, uuid.NewString(), ins.ID}, {app.OrgID, app.ID, uuid.NewString()}} {
		if _, err := s.GetInstanceApplicationStandardRuntimeQualification(ctx, scope[0], scope[1], scope[2]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unowned instance exposed qualification: %v", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.GetInstanceApplicationStandardRuntimeQualification(canceled, app.OrgID, app.ID, ins.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled qualification succeeded", err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeQualification(ctx, app.OrgID, app.ID, "invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("invalid instance id reached storage", err)
	}
	if err := s.SetComputeNodeActive(ctx, ins.NodeID, false); err != nil {
		t.Fatal(err)
	}
	standardRuntimeQualificationRead(t, s, ins, "native_consumer_unavailable")
	if err := s.SetComputeNodeActive(ctx, ins.NodeID, true); err != nil {
		t.Fatal(err)
	}
	if after := standardRuntimeQualificationRead(t, s, ins, ""); after.DesiredRevision != q.DesiredRevision {
		t.Fatal("qualification changed desired intent")
	}
	restarted := receipt.Binding
	restarted.Incarnation = uuid.NewString()
	registerConsumedNativeIdentity(t, s, restarted, runtimeadmission.ArtifactProtocolVersion)
	standardRuntimeQualificationRead(t, s, ins, "native_receipt_stale")
	if err := s.UpdateInstanceStateToTerminal(ctx, ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeQualification(ctx, app.OrgID, app.ID, ins.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("terminal instance qualified as a live consumer", err)
	}
}

func standardRuntimeQualificationScanSelection(t *testing.T, s standardRuntimeQualificationTestStore) {
	t.Helper()
	ins, receipt := issueConsumedNativeFixture(t, s)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt)
	if err != nil {
		t.Fatal(err)
	}
	standardRuntimeQualificationRead(t, s, ins, "")
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, ins.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	renewed := scan.Input
	renewed.ID = uuid.NewString()
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), renewed); err != nil {
		t.Fatal(err)
	}
	standardRuntimeQualificationRead(t, s, ins, "")
	failed := renewed
	failed.ID, failed.Status, failed.Reports, failed.Facts.Views, failed.Failure = uuid.NewString(), "failed", nil, nil, "scanner_unavailable"
	if _, err := s.PublishDeploymentRuntimeScan(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), ins.ID); err != nil {
		t.Fatal("renewable approval erased historical native consumption", err)
	}
	standardRuntimeQualificationRead(t, s, ins, "artifact_approval_stale")
}

func TestMemStandardRuntimeQualificationLifecycle(t *testing.T) {
	standardRuntimeQualificationLifecycle(t, NewMemStore())
}

func TestMemStandardRuntimeQualificationScanSelection(t *testing.T) {
	standardRuntimeQualificationScanSelection(t, NewMemStore())
}

func TestMemStandardRuntimeQualificationCurrentInputs(t *testing.T) {
	for _, change := range []string{"revision", "runtime", "publisher", "expiry", "receipt", "receipt_scope"} {
		t.Run(change, func(t *testing.T) {
			s := NewMemStore()
			ins, receipt := issueConsumedNativeFixture(t, s)
			ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt)
			if err != nil {
				t.Fatal(err)
			}
			standardRuntimeQualificationRead(t, s, ins, "")
			s.mu.Lock()
			want := "runtime_inputs_stale"
			switch change {
			case "revision":
				e := s.applicationStandardEnrollments[ins.AppID]
				e.DesiredRevision++
				e.PersistedRevision++
				s.applicationStandardEnrollments[ins.AppID] = e
			case "runtime":
				a := s.apps[ins.AppID]
				a.RAMMB++
				s.apps[ins.AppID] = a
			case "publisher":
				for key, signer := range s.trustedSigners {
					if sameStandardUUID(signer.AppID, ins.AppID) {
						delete(s.trustedSigners, key)
					}
				}
			case "expiry":
				key := s.deploymentRuntimeScanCurrent[canonicalStandardUUID(ins.DeploymentID)]
				scan := s.deploymentRuntimeScans[key]
				scan.ScannedAt = time.Now().Add(-api.ApplicationStandardArtifactScanTTL - time.Second).UTC()
				scan.ExpiresAt = scan.ScannedAt.Add(api.ApplicationStandardArtifactScanTTL)
				s.deploymentRuntimeScans[key] = scan
				want = "artifact_approval_stale"
			case "receipt":
				key := s.instanceApplicationStandardBootTokens[ins.ID]
				boot := s.instanceApplicationStandardBoots[key]
				boot.Receipt = nil
				s.instanceApplicationStandardBoots[key] = boot
				want = "native_receipt_stale"
			case "receipt_scope":
				key := s.instanceApplicationStandardBootTokens[ins.ID]
				boot := s.instanceApplicationStandardBoots[key]
				boot.Receipt.Binding.AppID = uuid.NewString()
				s.instanceApplicationStandardBoots[key] = boot
				want = "native_receipt_stale"
			}
			s.mu.Unlock()
			standardRuntimeQualificationRead(t, s, ins, want)
		})
	}
}
