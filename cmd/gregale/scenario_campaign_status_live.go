package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testCampaignStatusLiveBudget = 30 * time.Second

// A live status client exposes only reads. No cleanup or observation persistence
// function is reachable through this verification path.
type testCampaignStatusLiveReader interface {
	testCampaignConsumerReader
	testCampaignRegistrationReader
	testCampaignCleanupReader
}

type testCampaignStatusLiveResource struct {
	Kind          string                     `json:"kind"`
	RunID         string                     `json:"run_id"`
	Name          string                     `json:"name,omitempty"`
	State         string                     `json:"state"`
	CheckedAt     *time.Time                 `json:"checked_at,omitempty"`
	VerifiedAt    *time.Time                 `json:"verified_at,omitempty"`
	PreviousState string                     `json:"previous_state,omitempty"`
	Drift         string                     `json:"drift"`
	Issue         *testCampaignRecoveryIssue `json:"issue,omitempty"`
}

type testCampaignStatusLiveReport struct {
	StartedAt      time.Time                        `json:"started_at"`
	FinishedAt     time.Time                        `json:"finished_at"`
	State          string                           `json:"state"`
	EndpointSHA256 string                           `json:"api_endpoint_sha256,omitempty"`
	SnapshotStable bool                             `json:"snapshot_stable"`
	AllVerified    bool                             `json:"all_verified"`
	ResourcesClean bool                             `json:"resources_clean"`
	Resources      []testCampaignStatusLiveResource `json:"resources"`
	Issue          *testCampaignRecoveryIssue       `json:"issue,omitempty"`
	Notes          []string                         `json:"notes"`
}

type testCampaignStatusLiveTarget struct {
	Resource testCampaignStatusLiveResource
	Journal  *testCampaignRunJournal
	Consumer testCampaignFaultConsumer
	Session  testCampaignCleanupAction
}

func newTestCampaignStatusLiveReport() *testCampaignStatusLiveReport {
	return &testCampaignStatusLiveReport{StartedAt: time.Now().UTC(), State: "unverified", SnapshotStable: true, Resources: []testCampaignStatusLiveResource{}, Notes: []string{
		"Each verified state is a point-in-time API observation at its timestamp; these reads are not an atomic server snapshot.",
		"This report does not write cleanup observations, recover locks, revoke credentials or change test execution evidence.",
		"Resource verification never certifies missing execution receipts, coverage or a policy verdict.",
	}}
}

func testCampaignStatusLiveTargets(sources []testCampaignFaultSource) []testCampaignStatusLiveTarget {
	targets := []testCampaignStatusLiveTarget{}
	for _, source := range sources {
		for i := range source.Journals {
			j := source.Journals[i]
			targets = append(targets, testCampaignStatusLiveTarget{Resource: testCampaignStatusLiveResource{Kind: "registration", RunID: j.RunID}, Journal: &j})
			names := []string{}
			for name := range j.Faults.Consumers {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				targets = append(targets, testCampaignStatusLiveTarget{Resource: testCampaignStatusLiveResource{Kind: "consumer", RunID: j.RunID, Name: name}, Consumer: j.Faults.Consumers[name]})
			}
		}
		keys := []string{}
		for key := range source.Targets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			target := source.Targets[key]
			targets = append(targets, testCampaignStatusLiveTarget{Resource: testCampaignStatusLiveResource{Kind: "session", RunID: target.RunID, Name: target.Project}, Session: target})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i].Resource, targets[j].Resource
		return a.RunID+"/"+a.Kind+"/"+a.Name < b.RunID+"/"+b.Kind+"/"+b.Name
	})
	return targets
}

func testCampaignStatusLivePrevious(progress *testCampaignStatusWatchProgress) map[string]string {
	states := map[string]string{}
	if progress == nil {
		return states
	}
	for _, fault := range progress.Faults {
		states[fault.RunID+"/registration/"] = fault.Registration
		for name, state := range fault.Consumers {
			states[fault.RunID+"/consumer/"+name] = state
		}
	}
	for _, session := range progress.Sessions {
		states[session.RunID+"/session/"+session.Project] = session.Status
	}
	return states
}

func readTestCampaignStatusLiveResource(ctx context.Context, client testCampaignStatusLiveReader, target testCampaignStatusLiveTarget) (string, error) {
	switch target.Resource.Kind {
	case "consumer":
		c := target.Consumer
		live, err := readTestCampaignConsumer(ctx, client, target.Resource.RunID, target.Resource.Name, c)
		if errors.Is(err, errTestConsumerCancelled) {
			return "cancelled", nil
		}
		if errors.Is(err, errTestConsumerPending) {
			return "pending", nil
		}
		if isNotFound(err) {
			return "absent", nil
		}
		if err != nil {
			return "unverified", err
		}
		if (c.ID != "" && live.ID != c.ID) || live.AppID != c.AppID || live.ExternalRef != c.ExternalRef || live.ManagedByPlatformTenant || (live.Status != "active" && live.Status != "revoked") {
			return "unverified", errors.New("consumer identity changed")
		}
		return live.Status, nil
	case "registration":
		live, err := readTestCampaignRecoveryRegistration(ctx, client, target.Resource.RunID)
		if err != nil {
			return "unverified", err
		}
		if live.SchemaVersion != 1 || live.Members == nil {
			return "unverified", errors.New("registration lookup capability unavailable")
		}
		observation := testCampaignFaultObservation{}
		if err := observeTestCampaignRegistrationExpiry(&observation, live); err != nil {
			return "unverified", err
		}
		if len(live.Members) == 0 {
			return "absent", nil
		}
		j := target.Journal
		if j.Faults.Registration == "pending" || j.Faults.Registration == "deleted" {
			return "unverified", errors.New("registration exists outside saved intent")
		}
		members := map[string]string{}
		for name, workload := range j.Workloads {
			if workload.AppID == "" {
				return "unverified", errors.New("registration membership incomplete")
			}
			members[name] = workload.AppID
		}
		if api.ScenarioTestRegistrationSHA256(members) != api.ScenarioTestRegistrationSHA256(live.Members) {
			return "unverified", errors.New("registration membership changed")
		}
		return "present", nil
	case "session":
		live, err := readTestCampaignRecoverySession(ctx, client, target.Session.Project, target.Session.RunID)
		if isNotFound(err) {
			return "absent", nil
		}
		if err != nil {
			return "unverified", err
		}
		if live.App.ID != target.Session.AppID {
			return "unverified", errors.New("session identity changed")
		}
		return "present", nil
	}
	return "unverified", errors.New("unknown resource kind")
}

func verifyTestCampaignStatusLive(ctx context.Context, client testCampaignStatusLiveReader, sources []testCampaignFaultSource, progress *testCampaignStatusWatchProgress) *testCampaignStatusLiveReport {
	ctx, cancel := context.WithTimeout(ctx, testCampaignStatusLiveBudget)
	defer cancel()
	report := newTestCampaignStatusLiveReport()
	previous := testCampaignStatusLivePrevious(progress)
	for _, target := range testCampaignStatusLiveTargets(sources) {
		resource := target.Resource
		resource.State, resource.Drift = "unverified", "unverified"
		resource.PreviousState = previous[resource.RunID+"/"+resource.Kind+"/"+resource.Name]
		var err error
		if ctx.Err() != nil {
			err = testCampaignRecoveryContextError(ctx.Err(), 0)
		} else {
			resource.State, err = readTestCampaignStatusLiveResource(ctx, client, target)
			checked := time.Now().UTC()
			resource.CheckedAt = &checked
			if err == nil {
				resource.VerifiedAt = &checked
			}
		}
		if err != nil {
			resource.State = "unverified"
			resource.Issue = testCampaignRecoveryIssueFor(resource.Kind, "lookup", err)
		} else {
			previousState := resource.PreviousState
			if resource.Kind == "session" && previousState == "deleted" {
				previousState = "absent"
			}
			verifiedPrevious := previousState == "absent" || (resource.Kind == "consumer" && (previousState == "active" || previousState == "revoked" || previousState == "cancelled" || previousState == "pending")) || (resource.Kind != "consumer" && previousState == "present")
			if previousState == "" {
				resource.Drift = "no_previous_observation"
			} else if !verifiedPrevious {
				resource.Drift = "previous_unverified"
			} else {
				resource.Drift = "unchanged"
				if previousState != resource.State {
					resource.Drift = "changed"
				}
			}
		}
		report.Resources = append(report.Resources, resource)
	}
	report.AllVerified = len(report.Resources) > 0
	report.ResourcesClean = report.AllVerified
	for _, resource := range report.Resources {
		report.AllVerified = report.AllVerified && resource.VerifiedAt != nil
		clean := resource.State == "absent" || (resource.Kind == "consumer" && (resource.State == "revoked" || resource.State == "cancelled"))
		report.ResourcesClean = report.ResourcesClean && resource.VerifiedAt != nil && clean
	}
	if report.ResourcesClean {
		report.State = "verified_clean"
	} else if report.AllVerified {
		report.State = "verified_resources_present"
	}
	report.FinishedAt = time.Now().UTC()
	return report
}

func applyTestCampaignStatusLive(ctx context.Context, report *testCampaignStatusReport, paths testCampaignWatchPaths, plan testCampaignPolicyPlan, cp *testCampaignCheckpoint, scenarios map[string]testScenario, resolver *testCampaignBundleResolver) {
	result := newTestCampaignStatusLiveReport()
	report.LiveVerification = result
	blocked := func() {
		result.State = "blocked"
		result.Issue = testCampaignRecoveryIssueFor("inventory", "verify", errors.New("live status evidence unavailable"))
		result.FinishedAt = time.Now().UTC()
	}
	if cp == nil || !report.SnapshotStable || (report.Watcher != nil && report.Watcher.ReceiptPresent && !report.Watcher.EvidenceVerified) {
		blocked()
		return
	}
	sources, err := collectTestCampaignFaultHistory(plan, *cp, scenarios, paths.Directory, resolver)
	if err != nil {
		blocked()
		return
	}
	targets := testCampaignStatusLiveTargets(sources)
	for _, target := range targets {
		resource := target.Resource
		resource.State, resource.Drift = "unverified", "unverified"
		result.Resources = append(result.Resources, resource)
	}
	client, err := authedClient()
	if err != nil {
		blocked()
		return
	}
	if report.Watcher != nil && report.Watcher.ReceiptPresent {
		w, err := newTestCampaignWatchRecovery(paths)
		if err != nil {
			blocked()
			return
		}
		var receipt testCampaignWatchReceipt
		if readTestCampaignJSON(filepath.Join(paths.Directory, testCampaignWatchReceiptName), &receipt) != nil || w.BindingSHA256 != receipt.BindingSHA256 {
			blocked()
			return
		}
	}
	var progress *testCampaignStatusWatchProgress
	if report.Watcher != nil {
		progress = report.Watcher.Progress
	}
	result = verifyTestCampaignStatusLive(ctx, client, sources, progress)
	report.LiveVerification = result
	result.EndpointSHA256 = testCampaignJSONHash(client.BaseURL())
	fresh, err := collectTestCampaignFaultHistory(plan, *cp, scenarios, paths.Directory, resolver)
	if err != nil || testCampaignJSONHash(fresh) != testCampaignJSONHash(sources) {
		invalidateTestCampaignStatusLive(report)
	}
}

func invalidateTestCampaignStatusLive(report *testCampaignStatusReport) {
	live := report.LiveVerification
	if live == nil {
		return
	}
	report.SnapshotStable = false
	live.SnapshotStable = false
	live.AllVerified = false
	live.ResourcesClean = false
	live.State = "snapshot_changed"
	live.Issue = testCampaignRecoveryIssueFor("inventory", "verify", errors.New("live status snapshot changed"))
	for i := range live.Resources {
		live.Resources[i].State = "unverified"
		live.Resources[i].VerifiedAt = nil
		live.Resources[i].Drift = "unverified"
	}
}
