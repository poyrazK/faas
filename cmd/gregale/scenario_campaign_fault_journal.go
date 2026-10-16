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
	ID                   string    `json:"id,omitempty"`
	AppID                string    `json:"app_id"`
	AppSlug              string    `json:"app_slug"`
	ExternalRef          string    `json:"external_ref"`
	ExpiresAt            time.Time `json:"expires_at"`
	CreationIntentSHA256 string    `json:"creation_intent_sha256,omitempty"`
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
	RunID                   string            `json:"run_id"`
	Registration            string            `json:"registration"`
	Consumers               map[string]string `json:"consumers"`
	RegistrationExpiresAt   *time.Time        `json:"registration_expires_at,omitempty"`
	RegistrationExpiryState string            `json:"registration_expiry_state"`
	ConsumerIDs             map[string]string `json:"consumer_ids,omitempty"`
}

// Read and validate all journals before issuing even the first mutation.
func readTestCampaignFaultJournals(directory, batch string, scenario testScenario, portable ...bool) ([]testCampaignRunJournal, error) {
	paths, err := filepath.Glob(filepath.Join(testCampaignJournalDirectory(directory, batch), "*.json"))
	if err != nil {
		return nil, err
	}
	journals := []testCampaignRunJournal{}
	for _, path := range paths {
		if filepath.Base(path) == "binding.json" || (len(portable) == 1 && portable[0] && filepath.Base(path) == "inventory.json") {
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
		if c.CreationIntentSHA256 != "" {
			intent, err := api.ScenarioTestConsumerIntent(j.RunID, name, api.ScenarioTestConsumerRequest{AppID: c.AppID, ExpiresAt: c.ExpiresAt})
			if err != nil || intent != c.CreationIntentSHA256 {
				return errors.New("consumer creation intent differs from journal identity")
			}
		}
		if c.ID == "" && c.CreationIntentSHA256 == "" {
			return errors.New("consumer creation result is uncertain; inspect manually")
		}
		if id, err := uuid.Parse(c.ID); c.ID != "" && (err != nil || id.String() != c.ID) {
			return errors.New("invalid consumer ID")
		}
	}
	return nil
}
func inspectTestCampaignFaults(ctx context.Context, client testCampaignFaultClient, journals []testCampaignRunJournal, execute bool, registrationOnly bool) ([]testCampaignFaultObservation, error) {
	observations := []testCampaignFaultObservation{}
	for _, j := range journals {
		observation := testCampaignFaultObservation{RunID: j.RunID, Consumers: map[string]string{}, ConsumerIDs: map[string]string{}}
		names := []string{}
		for name := range j.Faults.Consumers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := j.Faults.Consumers[name]
			live, err := readTestCampaignConsumer(ctx, client, j.RunID, name, c)
			if c.ID == "" && err == nil {
				c.ID = live.ID
			}
			observation.ConsumerIDs[name] = c.ID
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
				revoked, err := revokeTestCampaignConsumer(ctx, client, j.RunID, name, c)
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
		}
		if err := observeTestCampaignRegistrationExpiry(&observation, live); err != nil {
			return observations, err
		}
		if len(live.Members) == 0 {
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
				if err := observeTestCampaignRegistrationExpiry(&observation, confirmed); err != nil {
					return observations, err
				}
			}
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// Expiry is informational: an elapsed deadline never replaces a live absence
// check, and older APIs with no expiry capability remain explicitly unknown.
func observeTestCampaignRegistrationExpiry(out *testCampaignFaultObservation, live api.ScenarioTestRegistration) error {
	state := live.ExpiryState
	if state == "" {
		state = "unknown"
		if len(live.Members) == 0 {
			state = "absent"
		}
	}
	switch state {
	case "pending", "expired":
		if len(live.Members) == 0 || live.ExpiresAt == nil || live.ExpiresAt.IsZero() {
			return errors.New("registration expiry metadata incomplete")
		}
	case "unknown":
		if len(live.Members) == 0 || live.ExpiresAt != nil {
			return errors.New("invalid unknown registration expiry")
		}
	case "absent":
		if len(live.Members) != 0 || live.ExpiresAt != nil {
			return errors.New("registration expiry does not prove absence")
		}
	default:
		return errors.New("unknown registration expiry state")
	}
	out.RegistrationExpiryState, out.RegistrationExpiresAt = state, live.ExpiresAt
	return nil
}

// Persist observations apart from immutable execution receipts. No credential
// value or assertion evidence can be generated by this resource cleanup.
func persistTestCampaignFaultObservation(directory string, observations []testCampaignFaultObservation) error {
	return persistTestCampaignProcess(filepath.Join(directory, "fault-cleanup-"+time.Now().UTC().Format("20060102T150405.000000000")+".json"), observations)
}

func applyTestCampaignFaultCompletion(ctx context.Context, completion *testCampaignRecoveryCompletion, reader testCampaignCleanupReader, plan testCampaignPolicyPlan, cp testCampaignCheckpoint, scenarios map[string]testScenario, directory string, hasAncestors bool, resolvers ...*testCampaignBundleResolver) {
	completion.FaultResourcesVerified = false
	completion.FaultResourcesAbsent = false
	fail := func() {
		completion.RecoveryComplete = false
		completion.Blockers = append(completion.Blockers, "Fault history, resource identities or current absence remain unverified.")
	}
	if hasAncestors != (plan.Recovery != nil) || len(resolvers) > 1 {
		fail()
		return
	}
	var resolver *testCampaignBundleResolver
	if len(resolvers) == 1 {
		resolver = resolvers[0]
	}
	client, ok := reader.(testCampaignFaultClient)
	if !ok {
		fail()
		return
	}
	sources, err := collectTestCampaignFaultHistory(plan, cp, scenarios, directory, resolver)
	if err != nil {
		fail()
		return
	}
	completion.FaultSources = sources
	completion.FaultHistorySnapshotSHA256 = testCampaignJSONHash(sources)
	observations, err := inspectTestCampaignFaults(ctx, client, testCampaignFaultHistoryJournals(sources), false, false)
	completion.FaultResources = observations
	if err != nil {
		fail()
		return
	}
	completion.FaultResourcesVerified = completion.ResourceEvidenceComplete
	completion.FaultResourcesAbsent = completion.ResourceEvidenceComplete
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
	for _, source := range sources {
		if source.Directory == directory {
			continue
		}
		for _, target := range source.Targets {
			if completion.LiveLookups >= 1024 {
				completion.KnownSessionsAbsent = false
				completion.FaultResourcesVerified = false
				continue
			}
			completion.LiveLookups++
			_, err := reader.GetDevSession(ctx, target.Project, target.RunID)
			if !isNotFound(err) {
				completion.KnownSessionsAbsent = false
				if err != nil {
					completion.FaultResourcesVerified = false
				}
			}
		}
	}
	fresh, err := collectTestCampaignFaultHistory(plan, cp, scenarios, directory, resolver)
	if err != nil || testCampaignJSONHash(fresh) != completion.FaultHistorySnapshotSHA256 {
		completion.FaultResourcesVerified = false
		completion.FaultResourcesAbsent = false
		completion.SnapshotStable = false
	}
	if !completion.FaultResourcesVerified || !completion.FaultResourcesAbsent || !completion.KnownSessionsAbsent {
		fail()
	}
}
