package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var testCampaignPortableJournalDirectory = regexp.MustCompile(`^batch-([A-Za-z0-9_-]+)\.journal$`)
var testCampaignPortableJournalRun = regexp.MustCompile(`^[0-9a-f]{32}\.json$`)

type testCampaignPortableJournalInventory struct {
	SchemaVersion int                               `json:"schema_version"`
	Files         []testCampaignBundleInventoryFile `json:"files"`
}

func testCampaignPortableJournalPath(name string) (string, string, bool) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "nodes" || !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(parts[1]) ||
		!testCampaignPortableJournalDirectory.MatchString(parts[2]) {
		return "", "", false
	}
	if parts[3] != "inventory.json" && parts[3] != "binding.json" && !testCampaignPortableJournalRun.MatchString(parts[3]) {
		return "", "", false
	}
	return strings.Join(parts[:3], "/"), parts[3], true
}

// The portable inventory binds the exact set of snapshots independently of
// decoding their contents; recovery never rewrites hash-bound source evidence.
func collectTestCampaignPortableJournal(nodePath, physical, batchID string, add func(string, []byte) error, portable ...bool) error {
	directory := testCampaignJournalDirectory(physical, batchID)
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := testCampaignPrivateDirectory(directory); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	allowInventory := len(portable) == 1 && portable[0]
	limit := 1025
	if allowInventory {
		limit++
	}
	if len(entries) == 0 || len(entries) > limit {
		return errors.New("journal requires binding and at most 1024 run snapshots")
	}
	if allowInventory {
		if _, err := validateTestCampaignFaultPortableInventory(directory); err != nil {
			return err
		}
	}
	prefix := nodePath + "/batch-" + batchID + ".journal"
	inventory := testCampaignPortableJournalInventory{SchemaVersion: 1, Files: []testCampaignBundleInventoryFile{}}
	for _, entry := range entries {
		name := entry.Name()
		if name == "inventory.json" && allowInventory {
			continue
		}
		if !entry.Type().IsRegular() || (name != "binding.json" && !testCampaignPortableJournalRun.MatchString(name)) {
			return errors.New("journal has an unexpected file or symlink")
		}
		body, err := readTestCampaignBundleBytes(filepath.Join(directory, name), 8*1024*1024)
		if err != nil {
			return err
		}
		if err := add(prefix+"/"+name, body); err != nil {
			return err
		}
		inventory.Files = append(inventory.Files, testCampaignBundleInventoryFile{Path: name, SHA256: testCampaignBundleHash(body)})
	}
	body, err := json.Marshal(inventory)
	if err != nil {
		return err
	}
	return add(prefix+"/inventory.json", body)
}

func validateTestCampaignPortableJournals(bundle testCampaignBundle, files map[string][]byte) error {
	directories := map[string]bool{}
	for name := range files {
		if directory, _, ok := testCampaignPortableJournalPath(name); ok {
			directories[directory] = true
		}
	}
	var plan testCampaignPolicyPlan
	if decodeTestCampaignBundleJSON(files["plan.json"], &plan) != nil {
		return errors.New("journal plan is invalid")
	}
	for directory := range directories {
		parts := strings.Split(directory, "/")
		var checkpoint testCampaignCheckpoint
		if decodeTestCampaignBundleJSON(files[strings.Join(parts[:2], "/")+"/checkpoint.json"], &checkpoint) != nil {
			return errors.New("journal references an absent checkpoint node")
		}
		batchID := testCampaignPortableJournalDirectory.FindStringSubmatch(parts[2])[1]
		if _, ok := checkpoint.Batches[batchID]; !ok {
			return errors.New("journal has no attempted checkpoint batch")
		}
		var binding testCampaignJournalBinding
		if decodeTestCampaignBundleJSON(files[directory+"/binding.json"], &binding) != nil || binding.SchemaVersion != 1 ||
			binding.PlanSHA256 != checkpoint.PlanSHA256 || binding.BatchID != batchID || binding.Side != checkpoint.Side ||
			binding.Revision != checkpoint.Revision || binding.Scenario == "" || binding.Profile == "" {
			return errors.New("portable journal binding differs from checkpoint")
		}
		if parts[1] == "0" {
			matched := false
			for _, batch := range plan.Batches {
				if batch.ID == batchID && batch.Side == binding.Side && batch.Scenario == binding.Scenario && batch.Profile == binding.Profile {
					matched = true
				}
			}
			if !matched {
				return errors.New("portable journal differs from its saved batch")
			}
		}
		var inventory testCampaignPortableJournalInventory
		if decodeTestCampaignBundleJSON(files[directory+"/inventory.json"], &inventory) != nil || inventory.SchemaVersion != 1 ||
			len(inventory.Files) == 0 || len(inventory.Files) > 1025 {
			return errors.New("portable journal inventory is missing or invalid")
		}
		seen := map[string]bool{}
		for _, entry := range inventory.Files {
			if seen[entry.Path] || (entry.Path != "binding.json" && !testCampaignPortableJournalRun.MatchString(entry.Path)) {
				return errors.New("portable journal inventory has duplicate or unsafe snapshot names")
			}
			body, ok := files[directory+"/"+entry.Path]
			if !ok || testCampaignBundleHash(body) != entry.SHA256 {
				return errors.New("portable journal snapshot is missing or changed")
			}
			seen[entry.Path] = true
			if entry.Path == "binding.json" {
				continue
			}
			var journal testCampaignRunJournal
			if decodeTestCampaignBundleJSON(body, &journal) != nil || journal.Binding != binding || journal.UpdatedAt.IsZero() ||
				entry.Path != journal.RunID+".json" || len(journal.Workloads) == 0 || len(journal.Workloads) > 1024 {
				return errors.New("portable run snapshot identity is invalid")
			}
			id, err := hex.DecodeString(journal.RunID)
			if err != nil || len(id) != 16 || hex.EncodeToString(id) != journal.RunID {
				return errors.New("portable run ID is invalid")
			}
			projects := map[string]bool{}
			for name, workload := range journal.Workloads {
				if name == "" || workload.Project == "" || projects[workload.Project] {
					return errors.New("portable workload identities are ambiguous")
				}
				projects[workload.Project] = true
				switch workload.State {
				case "pending", "creating":
					if workload.AppID != "" {
						return errors.New("unconfirmed workload carries an app identity")
					}
				case "provisioned", "cleanup_started", "cleanup_failed", "deleted":
					if workload.AppID == "" {
						return errors.New("portable workload lacks its returned app identity")
					}
				default:
					return errors.New("portable journal has an unknown lifecycle state")
				}
			}
		}
		if !seen["binding.json"] {
			return errors.New("portable journal inventory omits its binding")
		}
		for name := range files {
			if path.Dir(name) == directory && path.Base(name) != "inventory.json" && !seen[path.Base(name)] {
				return errors.New("portable journal contains an unlisted snapshot")
			}
		}
	}
	// Reject node paths beyond the declared ancestry, including otherwise valid
	// standalone journal files that could evade the checkpoint loop.
	for directory := range directories {
		found := false
		for _, node := range bundle.Nodes {
			if strings.HasPrefix(directory, node.Path+"/") {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("journal has no declared node")
		}
	}
	return nil
}
