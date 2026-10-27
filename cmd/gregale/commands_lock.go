package main

// --lock on job runs, crons and broker triggers — ADR-20261026145832: the
// command tree has no separate noun for exclusive keyed work. A job run,
// cron or trigger names its lock: the policy is the lane (a managed
// operation policy, `gregale operations policy`), and the key is the value
// that must run one at a time.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type exclusiveLock struct {
	Policy string
	Key    json.RawMessage
}

// takeLockFlags removes --lock POLICY and --lock-key JSON from args. The
// lock is nil when neither is present.
func takeLockFlags(args []string) (*exclusiveLock, []string, error) {
	var policy, key string
	var hasPolicy, hasKey bool
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--lock" && name != "--lock-key" {
			rest = append(rest, args[i])
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if name == "--lock" {
			policy, hasPolicy = value, true
		} else {
			key, hasKey = value, true
		}
	}
	switch {
	case !hasPolicy && !hasKey:
		return nil, rest, nil
	case operationsHidden():
		// The operations capability is hidden for the account
		// (ADR-20261023115741): --lock does not exist for it.
		return nil, nil, errors.New("gregale: unknown flag --lock")
	case !hasPolicy || policy == "":
		return nil, nil, errors.New("--lock-key needs --lock POLICY")
	case !hasKey:
		return nil, nil, errors.New(`--lock needs --lock-key JSON, the value that must run one at a time (such as '"customer-42"')`)
	}
	var scalar any
	if err := json.Unmarshal([]byte(key), &scalar); err != nil {
		return nil, nil, fmt.Errorf("--lock-key must be JSON: %w", err)
	}
	switch scalar.(type) {
	case string, float64, bool:
	default:
		return nil, nil, errors.New("--lock-key must be a JSON string, number, or boolean")
	}
	return &exclusiveLock{Policy: policy, Key: json.RawMessage(key)}, rest, nil
}

// operationsHidden reports that the operations capability, which owns
// exclusive keyed work, is hidden for the account.
func operationsHidden() bool {
	_, hidden := hiddenCommand([]string{"operations"}, false)
	return hidden
}

// bindExclusiveLock binds a created cron or broker trigger to its lock. The
// error names the created item and the command that retries the binding.
func bindExclusiveLock(ctx context.Context, client *Client, source, id string, lock *exclusiveLock) (*api.ExclusiveTriggerBindingRecord, error) {
	row, err := client.UpsertExclusiveTriggerBinding(ctx, source, id, api.ExclusiveTriggerBindingRequest{Policy: lock.Policy, Key: lock.Key})
	if err != nil {
		return nil, fmt.Errorf("created %s %s, but binding it to lock %s failed; retry with: gregale operations bind-trigger --policy %s --key '%s' %s %s: %w",
			source, id, lock.Policy, lock.Policy, lock.Key, source, id, err)
	}
	return &row, nil
}

const jobsRunLockedUsage = "usage: gregale jobs run <name> --lock POLICY --lock-key JSON [--tasks N | --run-file FILE] [--idempotency-key KEY]"

// cmdJobsRunLocked is `jobs run <name> --lock POLICY --lock-key JSON`: the
// run is submitted through the lock, as `operations start-job` does.
func cmdJobsRunLocked(lock *exclusiveLock, args []string) int {
	fs := newFlagSet("jobs-run", flag.ContinueOnError)
	tasks := fs.Int("tasks", 0, "number of tasks (default 1; omit with --run-file)")
	runFile := fs.String("run-file", "", "JSON CreateJobRunRequest for the other run settings")
	idempotencyKey := fs.String("idempotency-key", "", "stable retry identity for this submission")
	if err := parseInterspersed(fs, args); err != nil {
		_, _ = fmt.Fprintln(osStderr, "With --lock, jobs run takes --tasks, --run-file and --idempotency-key; put other run settings in --run-file.")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, jobsRunLockedUsage, "jobs")
		return 1
	}
	operation := []string{"--policy", lock.Policy, "--key", string(lock.Key)}
	if *tasks > 0 {
		operation = append(operation, "--tasks", strconv.Itoa(*tasks))
	}
	if *runFile != "" {
		operation = append(operation, "--run-file", *runFile)
	}
	if *idempotencyKey != "" {
		operation = append(operation, "--idempotency-key", *idempotencyKey)
	}
	return cmdOperationStartJob(append(operation, fs.Arg(0)))
}
