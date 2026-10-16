package main

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type testCampaignFaultConsumer struct {
	ID          string    `json:"id,omitempty"`
	AppID       string    `json:"app_id"`
	AppSlug     string    `json:"app_slug"`
	ExternalRef string    `json:"external_ref"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type testCampaignFaultJournal struct {
	Registration string                               `json:"registration"`
	Consumers    map[string]testCampaignFaultConsumer `json:"consumers"`
}

func (w *testCampaignJournalWriter) registration(state string) error {
	if w == nil {
		return nil
	}
	w.journal.Faults.Registration = state
	return w.save()
}
func (w *testCampaignJournalWriter) consumer(name string, c testCampaignFaultConsumer) error {
	if w == nil {
		return nil
	}
	w.journal.Faults.Consumers[name] = c
	return w.save()
}

type testCampaignFaultClient interface {
	GetScenarioTestRegistration(context.Context, string) (api.ScenarioTestRegistration, error)
	DeleteScenarioTestRegistration(context.Context, string, string) error
	GetAPIConsumer(context.Context, string, string) (api.APIConsumerResponse, error)
	RevokeAPIConsumer(context.Context, string, string) (api.APIConsumerResponse, error)
}
type testCampaignFaultObservation struct {
	RunID        string            `json:"run_id"`
	Registration string            `json:"registration"`
	Consumers    map[string]string `json:"consumers"`
}

// Read and validate all journals before issuing even the first mutation.
func readTestCampaignFaultJournals(directory, batch string, scenario testScenario) ([]testCampaignRunJournal, error) {
	paths, err := filepath.Glob(filepath.Join(testCampaignJournalDirectory(directory, batch), "*.json"))
	if err != nil {
		return nil, err
	}
	journals := []testCampaignRunJournal{}
	for _, path := range paths {
		if filepath.Base(path) == "binding.json" {
			continue
		}
		var j testCampaignRunJournal
		if err := readTestCampaignJSON(path, &j); err != nil {
			return nil, err
		}
		if err := validateTestCampaignFaultJournal(j, scenario); err != nil {
			return nil, err
		}
		journals = append(journals, j)
	}
	if len(journals) == 0 {
		return nil, errors.New("no fault journals available")
	}
	return journals, nil
}
func validateTestCampaignFaultJournal(j testCampaignRunJournal, scenario testScenario) error {
	decoded, decodeErr := hex.DecodeString(j.RunID)
	if decodeErr != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != j.RunID {
		return errors.New("invalid fault journal run identity")
	}
	if j.Faults == nil {
		return errors.New("legacy journal has no fault cleanup inventory")
	}
	switch j.Faults.Registration {
	case "pending", "registering", "registered", "deleted":
	default:
		return errors.New("invalid registration state")
	}
	if len(j.Faults.Consumers) > len(scenario.Consumers) {
		return errors.New("consumer inventory exceeds manifest")
	}
	for name, c := range j.Faults.Consumers {
		declared := false
		for _, spec := range scenario.Consumers {
			declared = declared || spec.Name == name
		}
		primary := j.Workloads[scenario.Project]
		if !declared || c.AppID == "" || c.AppID != primary.AppID || c.AppSlug == "" || c.ExternalRef != "gregale-test-"+j.RunID+"-"+name || c.ExpiresAt.IsZero() {
			return errors.New("consumer journal identity differs from workload/manifest")
		}
		if !api.ValidAppSlug(c.AppSlug) {
			return errors.New("invalid consumer app slug")
		}
		if c.ID == "" {
			return errors.New("consumer creation result is uncertain; inspect manually")
		}
		if id, err := uuid.Parse(c.ID); err != nil || id.String() != c.ID {
			return errors.New("invalid consumer ID")
		}
	}
	return nil
}
func inspectTestCampaignFaults(ctx context.Context, client testCampaignFaultClient, journals []testCampaignRunJournal, execute bool, registrationOnly bool) ([]testCampaignFaultObservation, error) {
	observations := []testCampaignFaultObservation{}
	for _, j := range journals {
		observation := testCampaignFaultObservation{RunID: j.RunID, Consumers: map[string]string{}}
		names := []string{}
		for name := range j.Faults.Consumers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := j.Faults.Consumers[name]
			live, err := client.GetAPIConsumer(ctx, c.AppSlug, c.ID)
			if isNotFound(err) {
				observation.Consumers[name] = "absent"
				continue
			}
			if err != nil {
				return observations, err
			}
			if live.ID != c.ID || live.AppID != c.AppID || live.ExternalRef != c.ExternalRef {
				return observations, errors.New("consumer identity changed")
			}
			if live.Status == "revoked" {
				observation.Consumers[name] = "revoked"
				continue
			}
			observation.Consumers[name] = "active"
			if execute && !registrationOnly {
				revoked, err := client.RevokeAPIConsumer(ctx, c.AppSlug, c.ID)
				if err != nil {
					return observations, err
				}
				if revoked.ID != c.ID || revoked.AppID != c.AppID || revoked.ExternalRef != c.ExternalRef || revoked.Status != "revoked" {
					return observations, errors.New("consumer revocation was not confirmed")
				}
				observation.Consumers[name] = "revoked"
			}
		}
		live, err := client.GetScenarioTestRegistration(ctx, j.RunID)
		if err != nil {
			return observations, err
		} else if live.SchemaVersion != 1 || live.Members == nil {
			return observations, errors.New("registration lookup capability unavailable")
		} else if len(live.Members) == 0 {
			observation.Registration = "absent"
		} else {
			if j.Faults.Registration == "pending" || j.Faults.Registration == "deleted" {
				return observations, errors.New("registration exists outside journaled intent")
			}
			members := map[string]string{}
			for name, w := range j.Workloads {
				if w.AppID == "" {
					return observations, errors.New("registration membership incomplete")
				}
				members[name] = w.AppID
			}
			digest := api.ScenarioTestRegistrationSHA256(members)
			if api.ScenarioTestRegistrationSHA256(live.Members) != digest {
				return observations, errors.New("registration membership changed")
			}
			observation.Registration = "present"
			if execute && registrationOnly {
				err = client.DeleteScenarioTestRegistration(ctx, j.RunID, digest)
				if err != nil && !isNotFound(err) {
					return observations, err
				}
				confirmed, lookupErr := client.GetScenarioTestRegistration(ctx, j.RunID)
				if lookupErr != nil || confirmed.SchemaVersion != 1 || confirmed.Members == nil || len(confirmed.Members) != 0 {
					return observations, errors.New("registration absence not confirmed")
				}
				observation.Registration = "absent"
			}
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// Persist observations apart from immutable execution receipts. No credential
// value or assertion evidence can be generated by this resource cleanup.
func persistTestCampaignFaultObservation(directory string, observations []testCampaignFaultObservation) error {
	return persistTestCampaignProcess(filepath.Join(directory, "fault-cleanup-"+time.Now().UTC().Format("20060102T150405.000000000")+".json"), observations)
}

func applyTestCampaignFaultCompletion(ctx context.Context, completion *testCampaignRecoveryCompletion, reader testCampaignCleanupReader, plan testCampaignPolicyPlan, cp testCampaignCheckpoint, scenarios map[string]testScenario, directory string, hasAncestors bool) {
	completion.FaultResourcesVerified = !hasAncestors && completion.ResourceEvidenceComplete
	completion.FaultResourcesAbsent = !hasAncestors && completion.ResourceEvidenceComplete
	if hasAncestors {
		completion.Blockers = append(completion.Blockers, "Ancestor fault inventories require separate verification; current work alone cannot certify them.")
	}
	faultClient, ok := reader.(testCampaignFaultClient)
	if !ok {
		completion.FaultResourcesAbsent = false
		completion.FaultResourcesVerified = false
	} else {
		for _, batch := range plan.Batches {
			if batch.Side != cp.Side {
				continue
			}
			if _, attempted := cp.Batches[batch.ID]; !attempted {
				continue
			}
			if _, _, err := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenarios[batch.Scenario]); err != nil {
				completion.FaultResourcesAbsent = false
				completion.FaultResourcesVerified = false
				continue
			}
			journals, err := readTestCampaignFaultJournals(directory, batch.ID, scenarios[batch.Scenario])
			if err != nil {
				completion.FaultResourcesAbsent = false
				completion.FaultResourcesVerified = false
				continue
			}
			observations, err := inspectTestCampaignFaults(ctx, faultClient, journals, false, false)
			completion.FaultResources = append(completion.FaultResources, observations...)
			if err != nil {
				completion.FaultResourcesAbsent = false
				completion.FaultResourcesVerified = false
			}
			for _, observation := range observations {
				if observation.Registration != "absent" {
					completion.FaultResourcesAbsent = false
				}
				for _, state := range observation.Consumers {
					if state != "absent" && state != "revoked" {
						completion.FaultResourcesAbsent = false
					}
				}
			}
		}
	}
	if !completion.FaultResourcesAbsent {
		completion.Blockers = append(completion.Blockers, "Fault registration or consumer absence remains unverified.")
		completion.RecoveryComplete = false
	}
}
