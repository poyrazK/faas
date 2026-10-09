package api

import "fmt"

// EnvironmentGitOpsJobSmokeMaxTimeoutSeconds keeps qualification work bounded
// independently of the larger customer job timeout allowed by some plans.
const EnvironmentGitOpsJobSmokeMaxTimeoutSeconds = 300

// Validate checks the reviewed argv contract without expanding shell syntax or
// applying customer-task defaults. Job qualification requires an explicit,
// short timeout so a missing value cannot turn into an unbounded run.
func (smoke EnvironmentJobSmoke) Validate() error {
	if smoke.TimeoutSeconds < 1 || smoke.TimeoutSeconds > EnvironmentGitOpsJobSmokeMaxTimeoutSeconds {
		return fmt.Errorf("timeout_seconds must be between 1 and %d", EnvironmentGitOpsJobSmokeMaxTimeoutSeconds)
	}
	if _, problem := (CreateAppTaskRequest{Command: smoke.Command, TimeoutSeconds: smoke.TimeoutSeconds}).Resolve(); problem != nil {
		return fmt.Errorf("command must be a bounded argv with a valid executable")
	}
	return nil
}
