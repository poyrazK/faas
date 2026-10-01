package state

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func policyJSON[T any](p *T) any {
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	return b
}

func sameWorkPolicy[T any](a, b *T) bool { return reflect.DeepEqual(a, b) }

func validateCronWorkPolicies(opts CronOptions) error {
	if opts.SchedulePolicy != nil {
		if err := opts.SchedulePolicy.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		if opts.SchedulePolicy.StartDeadlineSeconds > 30*24*60*60 {
			return fmt.Errorf("%w: schedule start deadline exceeds 30 days", ErrInvalidArgument)
		}
	}
	if opts.FailureRules != nil {
		if err := opts.FailureRules.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		encoded, err := json.Marshal(opts.FailureRules)
		if err != nil || len(encoded) > 16*1024 || len(opts.FailureRules.Rules) > 64 {
			return fmt.Errorf("%w: failure rules exceed the size limit", ErrInvalidArgument)
		}
	}
	return nil
}

func effectiveCronSchedulePolicy(cron Cron) *workpolicy.SchedulePolicy {
	if cron.SchedulePolicy != nil {
		return workpolicy.Clone(cron.SchedulePolicy)
	}
	policy := &workpolicy.SchedulePolicy{Version: workpolicy.Version, Overlap: "allow", MissedRuns: "skip"}
	if cron.SkipIfRunning {
		policy.Overlap = "skip"
	}
	return policy
}

func validateCronPolicyKind(opts CronOptions, commandCron bool) error {
	if commandCron || (opts.SchedulePolicy == nil && opts.FailureRules == nil) {
		return nil
	}
	return fmt.Errorf("%w: schedule policies and failure rules require a deployment command cron", ErrInvalidArgument)
}

// JobTaskCompletion carries the fenced observed result and policy decision in
// one write, so the immutable attempt journal cannot lose its classification.
type JobTaskCompletion struct {
	RunID, InstanceID, LeaseToken string
	TaskIndex                     int
	Status                        string
	ExitCode                      int
	ErrorClass, ErrorMessage      string
	LogContent                    string
	LogTruncated                  bool
	FinishedAt                    time.Time
	OutputManifest                json.RawMessage
	OutcomeCode                   string
	Decision                      *workpolicy.Decision
}

type JobTaskCompletionStore interface {
	CompleteJobTaskAttempt(context.Context, JobTaskCompletion) error
}
