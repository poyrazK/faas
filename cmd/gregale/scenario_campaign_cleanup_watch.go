package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// The grace timer is never ownership evidence. Recovery still requires the
// existing process identity verifier and exclusive claim/runner locks.
func watchTestCampaignCleanup(ctx context.Context, interval, grace time.Duration, attempts int,
	inspect func() (string, bool, error), recoverOwner func() bool, cleanup func() bool,
	observe func(string, int) error) int {
	var fingerprint string
	var since time.Time
	recovered := false
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx.Err() != nil {
			return 2
		}
		state := "waiting_for_owner"
		if !recovered {
			stamp, dead, err := inspect()
			if err != nil {
				_ = observe("manual_inspection_required", attempt)
				return 2
			}
			if !dead || stamp == "" {
				fingerprint = ""
				since = time.Time{}
			} else {
				if stamp != fingerprint {
					fingerprint, since = stamp, time.Now()
				}
				if time.Since(since) >= grace {
					if !recoverOwner() {
						_ = observe("manual_inspection_required", attempt)
						return 2
					}
					recovered = true
				} else {
					state = "owner_terminated_grace_period"
				}
			}
		}
		if recovered {
			if ctx.Err() != nil {
				return 2
			}
			state = "cleanup_unresolved"
			if cleanup() {
				if observe("cleanup_complete", attempt) != nil {
					return 2
				}
				return 0
			}
		}
		if observe(state, attempt) != nil {
			return 2
		}
		if attempt == attempts {
			break
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 2
		case <-timer.C:
		}
	}
	_ = observe("manual_inspection_required", attempts)
	return 2
}

func cmdTestCampaignCleanupWatch(args []string) int {
	fs := newFlagSet("test policy cleanup-watch", flag.ContinueOnError)
	plan := fs.String("plan", "", "bound campaign plan")
	policy := fs.String("policy", "", "bound campaign policy")
	manifest := fs.String("manifest", "gregale-test.yaml", "bound manifest")
	directory := fs.String("directory", "", "private interrupted campaign directory")
	handoff := fs.String("handoff", "", "active portable import index")
	interval := fs.Duration("interval", 30*time.Second, "delay between attempts (1s..1h)")
	grace := fs.Duration("grace", time.Minute, "stable verified termination period (1s..24h)")
	attempts := fs.Int("attempts", 120, "maximum polling/cleanup attempts (1..2880)")
	execute := fs.Bool("execute", false, "opt in to recovering a terminated owner and cleaning verified fault history")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 || rejectUnexpectedFlagArgs(fs) || !*execute || *plan == "" || *policy == "" || *directory == "" || *interval < time.Second || *interval > time.Hour || *grace < time.Second || *grace > 24*time.Hour || *attempts < 1 || *attempts > 2880 {
		return printErr("Invalid cleanup watcher options", errors.New("--execute, --plan, --policy and --directory required; durations and attempts must be within documented bounds"))
	}
	var err error
	*directory, err = filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid directory", err)
	}
	var original testCampaignPolicyPlan
	if err := readTestCampaignJSON(*plan, &original); err != nil {
		return printErr("Invalid plan", err)
	}
	planHash := testCampaignJSONHash(original)
	unchanged := func() bool {
		var current testCampaignPolicyPlan
		return readTestCampaignJSON(*plan, &current) == nil && testCampaignJSONHash(current) == planHash
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	inspect := func() (string, bool, error) {
		var p testCampaignPolicyPlan
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(*plan, &p); err != nil {
			return "", false, err
		}
		if testCampaignJSONHash(p) != planHash {
			return "", false, errors.New("watch target plan changed")
		}
		if err := readTestCampaignJSON(filepath.Join(*directory, "checkpoint.json"), &cp); err != nil {
			return "", false, err
		}
		if cp.SchemaVersion != 1 || cp.PlanSHA256 != testCampaignJSONHash(p) || !testCampaignCheckpointLineageMatches(cp, p) {
			return "", false, errors.New("checkpoint binding changed")
		}
		assessment, err := inspectTestCampaignRunnerLock(*directory, p, cp)
		return testCampaignJSONHash(assessment), assessment.Recoverable, err
	}
	recoverArgs := []string{"--plan", *plan, "--directory", *directory, "--handoff", *handoff, "--execute"}
	cleanupArgs := []string{"--plan", *plan, "--policy", *policy, "--manifest", *manifest, "--directory", *directory, "--handoff", *handoff, "--journal", "--faults", "--fault-history", "--execute"}
	observe := func(state string, attempt int) error {
		return json.NewEncoder(os.Stdout).Encode(struct {
			SchemaVersion int       `json:"schema_version"`
			State         string    `json:"watch_state"`
			Attempt       int       `json:"attempt"`
			ObservedAt    time.Time `json:"observed_at"`
		}{1, state, attempt, time.Now().UTC()})
	}
	return watchTestCampaignCleanup(ctx, *interval, *grace, *attempts, inspect,
		func() bool { return unchanged() && cmdTestCampaignRecoverLock(recoverArgs) == 0 },
		func() bool { return unchanged() && cmdTestCampaignControl("cleanup", cleanupArgs) == 0 }, observe)
}
