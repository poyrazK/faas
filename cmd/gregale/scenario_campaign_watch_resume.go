package main

import (
	"errors"
	"os"
	"path/filepath"
)

// Only exclusion left by a receipt owner is eligible. Process identity includes
// boot, PID namespace and start ticks; PID equality alone grants no authority.
func (w *testCampaignWatchRecovery) deadOwner(owner testCampaignProcessIdentity) bool {
	status, _ := probeTestCampaignProcess(owner)
	if status != "terminated" {
		return false
	}
	for _, previous := range append(append([]testCampaignProcessIdentity{}, w.Receipt.PreviousOwners...), w.Receipt.Owner) {
		if previous == owner {
			return true
		}
	}
	return false
}

func (w *testCampaignWatchRecovery) staleRunner() (string, error) {
	var lock testCampaignRunnerLock
	err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, "runner.lock"), &lock)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plan, cp, _, _, _, err := w.inputs()
	if err != nil {
		return "", err
	}
	if !w.deadOwner(lock.Coordinator) {
		return "", errors.New("runner exclusion is not owned by a terminated receipt owner")
	}
	result, err := inspectTestCampaignRunnerLock(w.Paths.Directory, plan, cp)
	if err != nil {
		return "", err
	}
	if !result.Recoverable || len(result.Workers) != 0 {
		return "", errors.New("watcher cannot recover exclusion with worker processes")
	}
	return result.LockSHA256, nil
}

func (w *testCampaignWatchRecovery) staleClaim() (string, error) {
	_, _, _, resolver, _, err := w.inputs()
	if err != nil {
		return "", err
	}
	if resolver == nil {
		return "", nil
	}
	var lock testCampaignClaimLock
	err = readTestCampaignJSON(resolver.Index.ClaimPath+".lock", &lock)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if lock.Directory != w.Paths.Directory || lock.PlanSHA256 == "" || lock.PlanSHA256 != w.currentPlanHash() || (lock.Operation != "cleanup" && lock.Operation != "recover-runner") || !w.deadOwner(lock.Owner) {
		return "", errors.New("handoff exclusion differs from watcher recovery scope")
	}
	result, err := inspectTestCampaignClaim(resolver)
	if err != nil {
		return "", err
	}
	if !result.Recoverable {
		return "", errors.New("handoff owner or workers are not verifiably terminated")
	}
	for _, row := range result.Work {
		if row.Runner != nil {
			if row.Directory != w.Paths.Directory {
				return "", errors.New("watcher cannot recover another work directory")
			}
			if _, err := w.staleRunner(); err != nil {
				return "", err
			}
		}
	}
	return result.LockSHA256, nil
}

func (w *testCampaignWatchRecovery) currentPlanHash() string {
	var plan testCampaignPolicyPlan
	if readTestCampaignJSON(w.Paths.Plan, &plan) != nil {
		return ""
	}
	return testCampaignJSONHash(plan)
}

func (w *testCampaignWatchRecovery) resumeStamp(terminated bool) (string, error) {
	if err := w.validate(terminated); err != nil {
		return "", err
	}
	runner, err := w.staleRunner()
	if err != nil {
		return "", err
	}
	claim, err := w.staleClaim()
	if err != nil {
		return "", err
	}
	return testCampaignJSONHash(struct {
		Receipt       *testCampaignWatchReceipt
		Runner, Claim string
	}{w.Receipt, runner, claim}), nil
}

func (w *testCampaignWatchRecovery) resume() error {
	if _, err := w.resumeStamp(true); err != nil {
		return err
	}
	// Publish the successor before releasing predecessors' exclusion. If this
	// process dies during recovery, the next successor can verify both identities.
	if err := w.transfer(); err != nil {
		return err
	}
	plan, _, _, resolver, _, err := w.inputs()
	if err != nil {
		return err
	}
	claim, err := w.staleClaim()
	if err != nil {
		return err
	}
	if claim != "" {
		result, err := releaseTestCampaignClaimChecked(resolver, func() error { _, err := w.resumeStamp(false); return err })
		if err != nil {
			return err
		}
		if !result.Released {
			return errors.New("watcher claim exclusion remains unresolved")
		}
	}
	runner, err := w.staleRunner()
	if err != nil {
		return err
	}
	if runner != "" {
		release, err := acquireTestCampaignHandoffFor(resolver, w.Paths.Directory, testCampaignJSONHash(plan), "recover-runner", false)
		if err != nil {
			return err
		}
		defer release()
		result, err := releaseTestCampaignRunnerLockChecked(w.Paths.Directory, plan, func() error {
			if err := w.validate(false); err != nil {
				return err
			}
			_, err := w.staleRunner()
			return err
		})
		if err != nil {
			return err
		}
		if !result.Released {
			return errors.New("watcher runner exclusion remains unresolved")
		}
	}
	return w.validate(false)
}

func (w *testCampaignWatchRecovery) recordInitial() error {
	plan, _, _, resolver, binding, err := w.inputs()
	if err != nil {
		return err
	}
	if binding != w.BindingSHA256 {
		return errors.New("watch inputs changed before receipt creation")
	}
	release, err := acquireTestCampaignHandoffFor(resolver, w.Paths.Directory, testCampaignJSONHash(plan), "cleanup", false)
	if err != nil {
		return err
	}
	defer release()
	releaseRunner, err := acquireTestCampaignRunnerLock(w.Paths.Directory, testCampaignJSONHash(plan))
	if err != nil {
		return err
	}
	defer releaseRunner()
	return w.create()
}
