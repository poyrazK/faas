package main

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const testCampaignJournalEnv = "GREGALE_INTERNAL_CAMPAIGN_JOURNAL"

type testCampaignJournalBinding struct {
	SchemaVersion int    `json:"schema_version"`
	PlanSHA256    string `json:"plan_sha256"`
	BatchID       string `json:"batch_id"`
	Side          string `json:"side"`
	Revision      string `json:"revision"`
	Scenario      string `json:"scenario"`
	Profile       string `json:"profile"`
}
type testCampaignJournalWorkload struct {
	Project string `json:"project"`
	AppID   string `json:"app_id,omitempty"`
	State   string `json:"state"`
}
type testCampaignRunJournal struct {
	Faults    *testCampaignFaultJournal              `json:"faults,omitempty"`
	Binding   testCampaignJournalBinding             `json:"binding"`
	RunID     string                                 `json:"run_id"`
	UpdatedAt time.Time                              `json:"updated_at"`
	Workloads map[string]testCampaignJournalWorkload `json:"workloads"`
}
type testCampaignJournalWriter struct {
	directory      string
	primaryProject string
	journal        testCampaignRunJournal
}

func testCampaignJournalDirectory(directory, batch string) string {
	return filepath.Join(directory, "batch-"+batch+".journal")
}
func prepareTestCampaignJournal(directory string, batch testCampaignCostBatch) (string, error) {
	var cp testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), &cp); err != nil {
		return "", err
	}
	if cp.SchemaVersion != 1 || cp.Batches[batch.ID].Status != "running" || cp.Side != batch.Side {
		return "", errors.New("journal requires a running checkpoint batch")
	}
	path := testCampaignJournalDirectory(directory, batch.ID)
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", err
	}
	binding := testCampaignJournalBinding{1, cp.PlanSHA256, batch.ID, cp.Side, cp.Revision, batch.Scenario, batch.Profile}
	if err := persistTestCampaignJournal(filepath.Join(path, "binding.json"), binding); err != nil {
		return "", err
	}
	parent, err := os.Open(directory)
	if err != nil {
		return "", err
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil {
		return "", syncErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}
func newTestCampaignRunJournal(directory, name, profile, runID string, scenario testScenario) (*testCampaignJournalWriter, error) {
	if directory == "" {
		return nil, nil
	}
	id, err := hex.DecodeString(runID)
	if err != nil || len(id) != 16 || hex.EncodeToString(id) != runID {
		return nil, errors.New("invalid native journal run ID")
	}
	if err := testCampaignPrivateDirectory(directory); err != nil {
		return nil, err
	}
	var binding testCampaignJournalBinding
	if err := readTestCampaignJSON(filepath.Join(directory, "binding.json"), &binding); err != nil {
		return nil, err
	}
	if binding.SchemaVersion != 1 || binding.Scenario != name || binding.Profile != profile {
		return nil, errors.New("native run differs from journal binding")
	}
	writer := &testCampaignJournalWriter{directory: directory, primaryProject: scenario.Project, journal: testCampaignRunJournal{
		Faults:  &testCampaignFaultJournal{Registration: "pending", Consumers: map[string]testCampaignFaultConsumer{}},
		Binding: binding, RunID: runID, Workloads: map[string]testCampaignJournalWorkload{}}}
	writer.journal.Workloads[scenario.Project] = testCampaignJournalWorkload{Project: scenario.Project, State: "pending"}
	for name := range scenario.Services {
		writer.journal.Workloads[name] = testCampaignJournalWorkload{Project: scenario.Project + "-" + name, State: "pending"}
	}
	path := filepath.Join(directory, runID+".json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := writer.save(); err != nil {
		return nil, err
	}
	return writer, nil
}
func persistTestCampaignJournal(path string, value any) error {
	if err := writeTestCampaignJSON(path, value); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr = directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
func (w *testCampaignJournalWriter) save() error {
	if w == nil {
		return nil
	}
	w.journal.UpdatedAt = time.Now().UTC()
	return persistTestCampaignJournal(filepath.Join(w.directory, w.journal.RunID+".json"), w.journal)
}
func (w *testCampaignJournalWriter) update(name, id, state string) error {
	if w == nil {
		return nil
	}
	workload, ok := w.journal.Workloads[name]
	if !ok {
		return errors.New("unknown journal workload")
	}
	if workload.AppID != "" && id != "" && workload.AppID != id {
		return errors.New("journal app identity changed")
	}
	if id != "" {
		workload.AppID = id
	}
	workload.State = state
	w.journal.Workloads[name] = workload
	return w.save()
}

// Journals authorize only resource inspection/cleanup, never assertion coverage.
func readTestCampaignJournalTargets(directory string, planHash string, cp testCampaignCheckpoint,
	batch testCampaignCostBatch, scenario testScenario, portable ...bool) (map[string]testCampaignCleanupAction, string, error) {
	path := testCampaignJournalDirectory(directory, batch.ID)
	if err := testCampaignPrivateDirectory(path); err != nil {
		return nil, "", err
	}
	expected := testCampaignJournalBinding{1, planHash, batch.ID, cp.Side, cp.Revision, batch.Scenario, batch.Profile}
	var binding testCampaignJournalBinding
	if err := readTestCampaignJSON(filepath.Join(path, "binding.json"), &binding); err != nil {
		return nil, "", err
	}
	if binding != expected {
		return nil, "", errors.New("journal binding differs from checkpoint")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, "", err
	}
	limit := 1025
	allowInventory := len(portable) == 1 && portable[0]
	if allowInventory {
		limit++
	}
	if len(entries) > limit {
		return nil, "", errors.New("journal exceeds 1024 runs")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	targets := map[string]testCampaignCleanupAction{}
	journals := []testCampaignRunJournal{}
	for _, entry := range entries {
		if entry.Name() == "binding.json" || (allowInventory && entry.Name() == "inventory.json") {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, "", errors.New("unexpected journal entry")
		}
		var journal testCampaignRunJournal
		if err := readTestCampaignJSON(filepath.Join(path, entry.Name()), &journal); err != nil {
			return nil, "", err
		}
		id, err := hex.DecodeString(journal.RunID)
		if err != nil || len(id) != 16 || hex.EncodeToString(id) != journal.RunID || entry.Name() != journal.RunID+".json" ||
			journal.Binding != expected || journal.UpdatedAt.IsZero() {
			return nil, "", errors.New("invalid run journal identity")
		}
		if journal.Faults != nil {
			if err := validateTestCampaignFaultJournal(journal, scenario); err != nil {
				return nil, "", err
			}
		}
		projects := map[string]string{scenario.Project: scenario.Project}
		for name := range scenario.Services {
			projects[name] = scenario.Project + "-" + name
		}
		if len(projects) != len(journal.Workloads) {
			return nil, "", errors.New("journal workload membership changed")
		}
		for name, project := range projects {
			workload, ok := journal.Workloads[name]
			if !ok || workload.Project != project {
				return nil, "", errors.New("journal project identity changed")
			}
			switch workload.State {
			case "pending":
				if workload.AppID != "" {
					return nil, "", errors.New("pending workload has an app ID")
				}
				continue
			case "creating":
				return nil, "", errors.New("provisioning result was not journaled; inspect the uncertain workspace manually")
			case "provisioned", "cleanup_started", "cleanup_failed", "deleted":
				if workload.AppID == "" {
					return nil, "", errors.New("journal lacks returned app identity")
				}
			default:
				return nil, "", errors.New("unknown journal workload state")
			}
			targets[project+"/"+journal.RunID] = testCampaignCleanupAction{Project: project, RunID: journal.RunID, AppID: workload.AppID}
		}
		journals = append(journals, journal)
	}
	if len(journals) == 0 {
		return nil, "", errors.New("no run journals available")
	}
	return targets, testCampaignJSONHash(journals), nil
}

func recordTestCampaignJournalCleanup(directory, batch string, actions []testCampaignCleanupAction) error {
	for _, action := range actions {
		state := ""
		switch action.Status {
		case "deleted", "absent":
			state = "deleted"
		case "delete_failed", "unconfirmed":
			state = "cleanup_failed"
		default:
			continue
		}
		path := testCampaignJournalDirectory(directory, batch)
		var journal testCampaignRunJournal
		if err := readTestCampaignJSON(filepath.Join(path, action.RunID+".json"), &journal); err != nil {
			return err
		}
		matched := false
		for name, workload := range journal.Workloads {
			if workload.Project == action.Project && workload.AppID == action.AppID {
				workload.State = state
				journal.Workloads[name] = workload
				matched = true
			}
		}
		if !matched {
			return errors.New("cleanup observation differs from journal")
		}
		writer := testCampaignJournalWriter{directory: path, journal: journal}
		if err := writer.save(); err != nil {
			return err
		}
	}
	return nil
}
