package apidserver

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// alertFailureFilterLookups is what validateAlertFailureFilter reads.
type alertFailureFilterLookups interface {
	JobGetByName(ctx context.Context, accountID, name string) (state.Job, error)
	CronByID(ctx context.Context, id string) (state.Cron, error)
}

// alertFailureFilterEnvironments resolves a stage rule's environment; both
// stores implement it.
type alertFailureFilterEnvironments interface {
	ProjectEnvironmentByID(ctx context.Context, id string) (state.ProjectEnvironment, error)
}

// alertFailureFilterChanged reports whether an update touches what
// validateAlertFailureFilter looks up. An edit that leaves the filter alone
// (a rename, disabling the rule) must still work after the job or cron it
// names is gone; the evaluator degrades such a rule instead.
func alertFailureFilterChanged(req api.UpdateAlertRuleRequest) bool {
	return req.JobName != nil || req.CronID != nil || req.Metric != nil
}

// validateAlertFailureFilter checks a failure rule's job or cron filter
// (ADR-045 amendment 1) on the rule as it will be stored: a job filter
// needs job_failures, a cron filter a failed_invocations rule with
// failure_source cron on an app. With lookup it also checks the job is a
// live job of the account and the cron a cron of the rule's app that fires
// in the rule's environment (production, or the rule's stage). The store
// re-checks the shape (alert_rules_job_name_chk, alert_rules_cron_id_chk);
// this names what is wrong.
func validateAlertFailureFilter(ctx context.Context, store alertFailureFilterLookups, rule state.AlertRule, lookup bool) *api.Problem {
	if p := alertFailureFilterShape(rule); p != nil || !lookup {
		return p
	}
	if rule.JobName != "" {
		if _, err := store.JobGetByName(ctx, rule.AccountID, rule.JobName); errors.Is(err, state.ErrNotFound) {
			return api.ErrAlertRuleInvalid("job_name " + rule.JobName + " is not a job of this account; see gregale job list")
		} else if err != nil {
			return api.ErrCapacity("could not check the alert's job")
		}
	}
	if rule.CronID == "" {
		return nil
	}
	cron, err := store.CronByID(ctx, rule.CronID)
	if errors.Is(err, state.ErrNotFound) || err == nil && cron.AppID != rule.AppID {
		return api.ErrAlertRuleInvalid("cron_id " + rule.CronID + " is not a cron of this app; see gregale cron list")
	}
	if err != nil {
		return api.ErrCapacity("could not check the alert's cron")
	}
	scope := ""
	if rule.EnvironmentID != "" {
		envs, ok := store.(alertFailureFilterEnvironments)
		if !ok {
			return api.ErrCapacity("could not check the alert's environment")
		}
		env, err := envs.ProjectEnvironmentByID(ctx, rule.EnvironmentID)
		if err != nil {
			return api.ErrCapacity("could not check the alert's environment")
		}
		scope = env.Slug
	}
	if cron.DeploymentScope != scope {
		return api.ErrAlertRuleInvalid("cron_id " + rule.CronID + " fires in another environment than this rule watches; create the rule in that environment")
	}
	return nil
}

// alertFailureFilterShape is the part of validateAlertFailureFilter that
// needs no lookup.
func alertFailureFilterShape(rule state.AlertRule) *api.Problem {
	if rule.JobName != "" {
		if rule.Metric != state.AlertMetricJobFailures {
			return api.ErrAlertRuleInvalid("job_name applies only to job_failures rules")
		}
		if len(rule.JobName) > api.AlertJobNameMaxBytes {
			return api.ErrAlertRuleInvalid("job_name is longer than 128 bytes")
		}
	}
	if rule.CronID == "" {
		return nil
	}
	if rule.Metric != state.AlertMetricFailedInvocs || rule.FailureSource != state.AlertFailureCron || rule.AppID == "" {
		return api.ErrAlertRuleInvalid("cron_id applies only to an app's failed_invocations rules with failure_source cron")
	}
	if _, err := uuid.Parse(rule.CronID); err != nil {
		return api.ErrAlertRuleInvalid("cron_id must be a cron id; see gregale cron list")
	}
	return nil
}
