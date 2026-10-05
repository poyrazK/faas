// adr: 435 — warm promotion uses fresh authority and cleanup cannot destroy a winner.

package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardNativePromotionTestVMM struct {
	*standardNativeTestVMM
	promotions      int
	beforePromotion func(runtimeadmission.Promotion)
}

type lostPromotionPublicationAckStore struct {
	*state.MemStore
	publications int
}

func (s *lostPromotionPublicationAckStore) PublishOwnedInstanceRuntime(ctx context.Context, p state.RuntimeInstancePublication) (state.Instance, error) {
	actual, err := s.MemStore.PublishOwnedInstanceRuntime(ctx, p)
	if err != nil || p.PromotionReceipt == nil {
		return actual, err
	}
	s.publications++
	if s.publications == 1 {
		return state.Instance{}, errors.New("simulated lost commit acknowledgment")
	}
	return actual, nil
}

func (v *standardNativePromotionTestVMM) PromoteAdmittedRuntime(_ context.Context, nodeID string, req *vmmdpb.PromoteAdmittedRuntimeRequest) (runtimeadmission.Receipt, error) {
	p, err := runtimeadmission.PromotionFromProto(req)
	if err != nil {
		return runtimeadmission.Receipt{}, err
	}
	if p.Validate(time.Now()) != nil || nodeID != v.identity.NodeID || p.Binding.Incarnation != v.identity.Incarnation {
		return runtimeadmission.Receipt{}, runtimeadmission.ErrInvalid
	}
	v.promotions++
	if v.beforePromotion != nil {
		v.beforePromotion(p)
	}
	r := p.Parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	return r, nil
}

func TestApplicationStandardNativeWarmPromotionRequiresDurableFreshGrant(t *testing.T) {
	for _, scenario := range []string{"same-lease", "process-changed-during-resume", "lost-publication-ack"} {
		t.Run(scenario, func(t *testing.T) {
			restart := scenario == "process-changed-during-resume"
			s, app, dep := newStandardCaptureService(t)
			target := 1
			if _, err := s.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetWarmPoolSize: true, WarmPoolSize: &target}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, Tier: state.SnapshotTierInit, FCVersion: "1.10.0", MemBytes: int64(app.RAMMB) << 20, StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "native-promotion")}); err != nil {
				t.Fatal(err)
			}
			v := &standardNativePromotionTestVMM{standardNativeTestVMM: newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s))}
			var engineStore state.Store = s
			ackStore := &lostPromotionPublicationAckStore{MemStore: s}
			if scenario == "lost-publication-ack" {
				engineStore = ackStore
			}
			e := newEngine(t, engineStore, v, &fakeNotifier{}, "1.10.0")
			if err := e.ReconcileWarmPool(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			rows, _ := s.ListInstancesForApp(t.Context(), app.ID)
			if len(rows) != 1 || rows[0].State != string(state.StateWarm) {
				t.Fatalf("initial paused restore: %+v", rows)
			}
			warm := rows[0]
			v.beforePromotion = func(p runtimeadmission.Promotion) {
				saved, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), p)
				if err != nil || !saved.Equal(p) || p.Binding.Token == p.Parent.Binding.Token {
					t.Fatalf("resume preceded saved fresh grant: %+v %v", saved, err)
				}
				if restart {
					if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: warm.NodeID, Incarnation: uuid.NewString()}); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := e.Wake(t.Context(), app.ID, dep.ID, "", TriggerGateway)
			actual, _ := s.InstanceByID(t.Context(), warm.ID)
			if restart {
				if err == nil || actual.State == string(state.StateRunning) || v.destroys == 0 || e.Ledger().ResidentRAM() != 0 {
					t.Fatalf("old process resume published/leaked: %+v %v state=%s RAM=%d", result, err, actual.State, e.Ledger().ResidentRAM())
				}
			} else if err != nil || result.InstanceID != warm.ID || actual.State != string(state.StateRunning) || actual.Netns != warm.Netns || v.promotions != 1 || v.restores != 1 || e.Ledger().Concurrency(app.ID) != 1 {
				t.Fatalf("warm native promotion: %+v %v state=%s resumes=%d restores=%d", result, err, actual.State, v.promotions, v.restores)
			}
			if !restart {
				if e.discardWarmPromotion(t.Context(), warm, "delayed-other-caller") || v.destroys != 0 || e.Ledger().Concurrency(app.ID) != 1 {
					t.Fatal("late cleanup destroyed/released committed promotion")
				}
			}
			enrollment, _ := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
			if scenario == "lost-publication-ack" && (ackStore.publications != 2 || v.promotions != 1 || v.destroys != 0) {
				t.Fatalf("lost ack repeated resume or destroyed committed lease: publications=%d resumes=%d destroys=%d", ackStore.publications, v.promotions, v.destroys)
			}
			if enrollment.ObservedRevision != 0 {
				t.Fatal("resume fabricated policy observation")
			}
		})
	}
}
