package state

// adr: 634
import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostport"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

// InstanceByID deliberately omits terminal_at in PostgreSQL. Verify the actual
// retention anchor through the same reader used by the retention sweep.
func regionalFailureTerminalAt(t *testing.T, s Store, instance string) *time.Time {
	t.Helper()
	rows, err := s.ListInstancesInTerminalStatesOlderThan(t.Context(), []State{StateColdBooting, StateFailed, StateStopped}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range rows {
		if runtimequota.MatchesID(regionalQuotaCanonicalID(instance), ins.ID) {
			return ins.TerminalAt
		}
	}
	return nil
}

func regionalAttemptFailureFixture(t *testing.T, s Store, account Account, cell string) (regionalCapacityFixture, Instance) {
	t.Helper()
	var ins Instance
	f := newRegionalCapacityFixture(t, s, s.(RegionalRuntimeCapacityLedger), account, cell, func(q regionalQuotaFixture) {
		dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: q.grant.Binding.RegionalAppID, Kind: DeploymentKindImage, ImageDigest: "sha256:abc", Status: DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		region := "eu-fsn"
		node, err := s.CreateComputeNode(t.Context(), ComputeNode{ID: uuid.NewString(), Name: "failed-launch-" + uuid.NewString(), TargetURL: "unix:///failed-launch-test", Region: &region, Active: true,
			VPCPUs: 1, VCPUBudget: 1, MemMB: api.RAMAdmissionCeilingMB, AdmissionCeilingMB: api.RAMAdmissionCeilingMB, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		ins, err = s.CreateInstance(t.Context(), dep.AppID, dep.ID, string(StateColdBooting), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
	})
	return f, ins
}

func runRegionalAttemptFailureContract(t *testing.T, fixture func(*testing.T) (regionalCapacityFixture, Instance)) {
	t.Helper()
	for _, change := range []string{"exact", "wrong-state", "wrong-source", "no-stop", "resumed", "wrong-run", "wrong-node", "wrong-deployment"} {
		t.Run(change, func(t *testing.T) {
			f, ins := fixture(t)
			s := f.ledger.(Store)
			p := f.permit(runtimequota.CapacityWarm)
			p.InstanceID, p.RunID, p.NodeID, p.DeploymentID = regionalQuotaCanonicalID(ins.ID), regionalQuotaCanonicalID(ins.WakeID), regionalQuotaCanonicalID(ins.NodeID), regionalQuotaCanonicalID(ins.DeploymentID)
			switch change {
			case "wrong-run":
				p.RunID = uuid.NewString()
			case "wrong-node":
				p.NodeID = uuid.NewString()
			case "wrong-deployment":
				p.DeploymentID = uuid.NewString()
			}
			source := runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}
			if err := s.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: p}, source); err != nil {
				t.Fatal(err)
			}
			ports := s.(HostPortLeaseStore)
			if _, err := ports.AcquireHostPortLeases(t.Context(), ins.NodeID, ins.ID, []hostport.Request{{Name: "web", Protocol: hostport.TCP, GuestPort: 8080}}); err != nil {
				t.Fatal(err)
			}
			if change == "resumed" {
				if _, err := s.IssueRegionalRuntimeServingResume(t.Context(), resumeIntent(t, p)); err != nil {
					t.Fatal(err)
				}
			}
			r, err := s.BeginRegionalRuntimeLaunchFailure(t.Context(), source, uuid.NewString())
			if change == "resumed" || change == "wrong-run" || change == "wrong-node" || change == "wrong-deployment" {
				if !errors.Is(err, ErrConflict) {
					t.Fatal("stale initial launch fenced newer ownership", err)
				}
				before, err := s.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
				if err != nil || before.FenceID != "" || before.ReleasedAt != nil {
					t.Fatal("refused cleanup mutated physical hold", before, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.BeginRegionalRuntimeLaunchFailure(t.Context(), source, uuid.NewString())
			if err != nil || again.FenceID != r.FenceID {
				t.Fatal("first failure fence changed", again, err)
			}
			if change != "no-stop" {
				f.stop(t, p, r.FenceID)
			}
			expected := ins.State
			if change == "wrong-state" {
				expected = string(StateRunning)
			}
			if change == "wrong-source" {
				source.InstanceKey = p.InstanceID
				if source.InstanceKey == ins.ID {
					source.InstanceKey = strings.ReplaceAll(p.InstanceID, "-", "")
				}
			}
			err = s.FailRegionalRuntimeAttempt(t.Context(), source, expected)
			after, readErr := s.InstanceByID(t.Context(), ins.ID)
			leases, leaseErr := ports.ListHostPortLeases(t.Context(), ins.NodeID, ins.ID)
			if readErr != nil || leaseErr != nil {
				t.Fatal(readErr, leaseErr)
			}
			after.TerminalAt = regionalFailureTerminalAt(t, s, ins.ID)
			if change != "exact" {
				if err == nil || after.State != ins.State || after.TerminalAt != nil || len(leases) != 1 {
					t.Fatal("uncertain/stale stop changed original row or leases", after, leases, err)
				}
				return
			}
			if err != nil || after.State != string(StateFailed) || after.TerminalAt == nil || len(leases) != 0 {
				t.Fatal("accepted stop did not complete original row and leases", after, leases, err)
			}
			terminal := *after.TerminalAt
			if err := s.FailRegionalRuntimeAttempt(t.Context(), source, after.State); err != nil {
				t.Fatal(err)
			}
			retry, err := s.InstanceByID(t.Context(), ins.ID)
			retry.TerminalAt = regionalFailureTerminalAt(t, s, ins.ID)
			if err != nil || retry.TerminalAt == nil || !retry.TerminalAt.Equal(terminal) {
				t.Fatal("retry changed terminal time", retry, err)
			}
			rows, err := s.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), RegionalRuntimeRetirementCursor{}, 10)
			if err != nil || len(rows) != 0 {
				t.Fatal("completed row remained a recovery candidate", rows, err)
			}
		})
	}
	t.Run("accepted-stop-retries-row-completion", func(t *testing.T) {
		f, ins := fixture(t)
		p := f.permit(runtimequota.CapacityPrime)
		p.InstanceID, p.RunID, p.NodeID, p.DeploymentID = regionalQuotaCanonicalID(ins.ID), regionalQuotaCanonicalID(ins.WakeID), regionalQuotaCanonicalID(ins.NodeID), regionalQuotaCanonicalID(ins.DeploymentID)
		source := runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}
		if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: p}, source); err != nil {
			t.Fatal(err)
		}
		r, err := f.ledger.BeginRegionalRuntimeLaunchFailure(t.Context(), source, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		f.stop(t, p, r.FenceID)
		rows, err := f.ledger.ListRegionalRuntimeRetirements(t.Context(), f.cell.IssuerID(), RegionalRuntimeRetirementCursor{}, 10)
		if err != nil || len(rows) != 1 || rows[0].Source != source {
			t.Fatal("accepted stop lost unfinished scheduler effects", rows, err)
		}
		if err := f.ledger.FailRegionalRuntimeAttempt(t.Context(), source, ins.State); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("failure-fence-races-serving-resume", func(t *testing.T) {
		f, ins := fixture(t)
		p := f.permit(runtimequota.CapacityWarm)
		p.InstanceID, p.RunID, p.NodeID, p.DeploymentID = regionalQuotaCanonicalID(ins.ID), regionalQuotaCanonicalID(ins.WakeID), regionalQuotaCanonicalID(ins.NodeID), regionalQuotaCanonicalID(ins.DeploymentID)
		source := runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}
		if err := f.ledger.IssueRegionalRuntimeLaunch(t.Context(), runtimequota.Placement{Permit: p}, source); err != nil {
			t.Fatal(err)
		}
		intent := resumeIntent(t, p)
		start, fenced, resumed := make(chan struct{}), make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			_, err := f.ledger.BeginRegionalRuntimeLaunchFailure(t.Context(), source, uuid.NewString())
			fenced <- err
		}()
		go func() {
			<-start
			_, err := f.ledger.(RegionalRuntimeServingResumeStore).IssueRegionalRuntimeServingResume(t.Context(), intent)
			resumed <- err
		}()
		close(start)
		fenceErr, resumeErr := <-fenced, <-resumed
		if (fenceErr == nil) == (resumeErr == nil) {
			t.Fatal("failure fence and newer resume did not exclude each other", fenceErr, resumeErr)
		}
		if fenceErr != nil && !errors.Is(fenceErr, ErrConflict) {
			t.Fatal(fenceErr)
		}
		if resumeErr != nil && !errors.Is(resumeErr, ErrRegionalRuntimePlacementUnavailable) {
			t.Fatal(resumeErr)
		}
	})
}

func TestMemRegionalRuntimeAttemptFailureContract(t *testing.T) {
	runRegionalAttemptFailureContract(t, func(t *testing.T) (regionalCapacityFixture, Instance) {
		s := NewMemStore()
		account, err := s.CreateAccount(t.Context(), "failed-launch@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return regionalAttemptFailureFixture(t, s, account, uuid.NewString())
	})
}
