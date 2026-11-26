package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/flagnotify"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Flag notification hooks (ADR-20261125085913 §2, §3): the experiment and
// lifecycle reconcilers queue notifications for meterd to deliver.

const (
	// flagNotifyCheckInterval is how often one experiment's recommendation
	// is computed for a decision notification: the results report is
	// heavy and a decision an hour late is still on time.
	flagNotifyCheckInterval = time.Hour
	// flagNotifyTimeout bounds one notification check, apart from the
	// pass's other steps.
	flagNotifyTimeout = 30 * time.Second
)

// flagNotifyStore returns the notification store when flag notifications
// are released to subject.
func (s *server) flagNotifyStore(subject platformflags.Subject) (state.FlagNotificationStore, bool) {
	store, ok := s.store.(state.FlagNotificationStore)
	if !ok || !s.platformFlagFor(platformflags.FlagNotifications, subject) {
		return nil, false
	}
	return store, true
}

// environmentNotifyChannels are an environment's notification channels;
// none when it has no settings.
func environmentNotifyChannels(ctx context.Context, store state.FlagNotificationStore, environmentID string) ([]string, error) {
	st, err := store.FlagNotificationSettings(ctx, environmentID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	return st.ChannelIDs, err
}

// experimentNotifyTarget is where an experiment's notifications go.
type experimentNotifyTarget struct {
	store    state.FlagNotificationStore
	where    flagnotify.Where
	channels []string
	own      bool
}

// experimentNotifyTarget resolves the experiment's channels: its own that
// still exist, else the environment's. ok is false when flag notifications
// are off for the project's owner or there are no channels.
func (r *flagExperimentReconciler) experimentNotifyTarget(ctx context.Context, x state.FlagExperiment) (experimentNotifyTarget, bool, error) {
	s := r.server
	project, err := s.store.ProjectByID(ctx, x.ProjectID)
	if err != nil {
		return experimentNotifyTarget{}, false, fmt.Errorf("project: %w", err)
	}
	store, ok := s.flagNotifyStore(platformflags.Subject{AccountID: x.AccountID, Plan: x.Plan, OrgID: project.OrgID})
	if !ok {
		return experimentNotifyTarget{}, false, nil
	}
	var liveOwn []string
	if len(x.NotifyChannelIDs) > 0 {
		chans, err := store.FlagNotificationChannels(ctx, x.AccountID, x.NotifyChannelIDs)
		if err != nil {
			return experimentNotifyTarget{}, false, err
		}
		for _, c := range chans {
			liveOwn = append(liveOwn, c.ID)
		}
	}
	envChannels, err := environmentNotifyChannels(ctx, store, x.EnvironmentID)
	if err != nil {
		return experimentNotifyTarget{}, false, err
	}
	channels, own := flagnotify.Channels(liveOwn, envChannels)
	if len(channels) == 0 {
		return experimentNotifyTarget{}, false, nil
	}
	return experimentNotifyTarget{store: store, channels: channels, own: own, where: flagnotify.Where{
		AccountID: x.AccountID, EnvironmentID: x.EnvironmentID, OrgID: project.OrgID,
		Project: project.Slug, Environment: x.EnvironmentSlug}}, true, nil
}

// notifyDue reports whether the experiment's recommendation is due a check
// and marks it checked; entries for experiments not seen for a while go.
func (r *flagExperimentReconciler) notifyDue(id string, now time.Time) bool {
	if r.notifyChecked == nil {
		r.notifyChecked = map[string]time.Time{}
	}
	if last, ok := r.notifyChecked[id]; ok && now.Sub(last) < flagNotifyCheckInterval {
		return false
	}
	for k, t := range r.notifyChecked {
		if now.Sub(t) > 2*flagNotifyCheckInterval {
			delete(r.notifyChecked, k)
		}
	}
	r.notifyChecked[id] = now
	return true
}

// notify queues the running experiment's recommendation when it needs a
// human: a decision or a sample ratio mismatch, once per action and
// variant. It checks each experiment at most once per
// flagNotifyCheckInterval, under its own timeout. A guardrail rollback is
// queued where it happens (notifyRollback).
func (r *flagExperimentReconciler) notify(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	if !r.notifyDue(x.ID, now) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, flagNotifyTimeout)
	defer cancel()
	target, ok, err := r.experimentNotifyTarget(ctx, x)
	if err != nil || !ok {
		return err
	}
	fresh, err := r.store.GetFlagExperiment(ctx, x.EnvironmentID, x.Key)
	if err != nil {
		return err
	}
	if fresh.Status != flags.ExperimentRunning {
		return nil
	}
	report, err := loadFlagExperimentReport(ctx, r.store, fresh, r.server.productAnalyticsOn(x.AccountID, x.Plan))
	if err != nil {
		return err
	}
	rec := flagExperimentResultsResponse(fresh, report, target.where.Project, target.where.Environment).Recommendation
	return target.enqueue(ctx, flagnotify.ExperimentInput{
		Where: target.where, Experiment: x.Key, Flag: x.FlagKey,
		Action: rec.Action, Variant: rec.Variant, Summary: rec.Summary, Commands: rec.Commands,
	}, now)
}

// notifyRollback queues the guardrail breach problem. It runs before the
// experiment is stopped: when queueing fails the experiment stays running
// and the next pass, which finds the same breach, tries again (serving
// control again is a no-op).
func (r *flagExperimentReconciler) notifyRollback(ctx context.Context, x state.FlagExperiment, guardrail, variant string, rolledBack bool, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flagNotifyTimeout)
	defer cancel()
	target, ok, err := r.experimentNotifyTarget(ctx, x)
	if err != nil || !ok {
		return err
	}
	rec := flags.Recommend(flags.RecommendInput{StopReason: flags.StopGuardrailBreach})
	summary := fmt.Sprintf("the %s guardrail was breached by variant %s, so %s", guardrail, variant, rec.Summary)
	if !rolledBack {
		summary = fmt.Sprintf("the %s guardrail was breached by variant %s and the experiment stopped, but its rule changed since it started, so customers were not moved to control; check the flag", guardrail, variant)
	}
	return target.enqueue(ctx, flagnotify.ExperimentInput{
		Where: target.where, Experiment: x.Key, Flag: x.FlagKey, Action: rec.Action, Summary: summary,
	}, now)
}

func (t experimentNotifyTarget) enqueue(ctx context.Context, in flagnotify.ExperimentInput, now time.Time) error {
	in.OwnChannels = t.own
	n, ok := flagnotify.Experiment(in, t.channels)
	if !ok {
		return nil
	}
	_, err := t.store.EnqueueFlagNotification(ctx, n, now)
	return err
}

// notifyLifecycle queues an environment's weekly digest and its flag
// expiries when the environment has channels.
func (r *featureFlagLifecycleReconciler) notifyLifecycle(ctx context.Context, acct state.Account, env state.FeatureFlagAutoRolloutCandidate, report []flags.FlagLifecycle, now time.Time) error {
	project, err := r.server.store.ProjectByID(ctx, env.Scope.ProjectID)
	if err != nil {
		return fmt.Errorf("project: %w", err)
	}
	store, ok := r.server.flagNotifyStore(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: project.OrgID})
	if !ok {
		return nil
	}
	channels, err := environmentNotifyChannels(ctx, store, env.Scope.EnvironmentID)
	if err != nil || len(channels) == 0 {
		return err
	}
	where := flagnotify.Where{AccountID: env.Scope.AccountID, EnvironmentID: env.Scope.EnvironmentID, OrgID: project.OrgID,
		Project: env.ProjectSlug, Environment: env.EnvironmentSlug}
	queue := flagnotify.Expired(where, report, channels)
	if digest, ok := flagnotify.Digest(where, report, channels, now); ok {
		queue = append(queue, digest)
	}
	for _, n := range queue {
		if _, err := store.EnqueueFlagNotification(ctx, n, now); err != nil {
			return err
		}
	}
	return nil
}
