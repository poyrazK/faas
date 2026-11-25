package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/flagnotify"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Flag notification hooks (ADR-20261125085913 §2, §3): the experiment and
// lifecycle reconcilers queue notifications for meterd to deliver.

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

// notify queues the experiment's recommendation when it needs a human: a
// decision or a problem, once per action and variant. It reads the
// experiment again, so a guardrail rollback in this pass is seen.
func (r *flagExperimentReconciler) notify(ctx context.Context, x state.FlagExperiment, now time.Time) error {
	s := r.server
	store, ok := s.flagNotifyStore(platformflags.Subject{AccountID: x.AccountID, Plan: x.Plan})
	if !ok {
		return nil
	}
	envChannels, err := environmentNotifyChannels(ctx, store, x.EnvironmentID)
	if err != nil {
		return err
	}
	channels := flagnotify.Channels(x.NotifyChannelIDs, envChannels)
	if len(channels) == 0 {
		return nil
	}
	fresh, err := r.store.GetFlagExperiment(ctx, x.EnvironmentID, x.Key)
	if err != nil {
		return err
	}
	project, err := s.store.ProjectByID(ctx, x.ProjectID)
	if err != nil {
		return err
	}
	report, err := loadFlagExperimentReport(ctx, r.store, fresh, s.productAnalyticsOn(x.AccountID, x.Plan))
	if err != nil {
		return err
	}
	rec := flagExperimentResultsResponse(fresh, report, project.Slug, x.EnvironmentSlug).Recommendation
	n, ok := flagnotify.Experiment(flagnotify.ExperimentInput{
		Where:      flagnotify.Where{AccountID: x.AccountID, EnvironmentID: x.EnvironmentID, Project: project.Slug, Environment: x.EnvironmentSlug},
		Experiment: x.Key, Flag: x.FlagKey, Action: rec.Action, Variant: rec.Variant, Summary: rec.Summary, Commands: rec.Commands,
	}, channels)
	if !ok {
		return nil
	}
	_, err = store.EnqueueFlagNotification(ctx, n, now)
	return err
}

// notifyLifecycle queues an environment's weekly digest and its flag
// expiries when the environment has channels.
func (r *featureFlagLifecycleReconciler) notifyLifecycle(ctx context.Context, acct state.Account, env state.FeatureFlagAutoRolloutCandidate, report []flags.FlagLifecycle, now time.Time) error {
	store, ok := r.server.flagNotifyStore(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
	if !ok {
		return nil
	}
	channels, err := environmentNotifyChannels(ctx, store, env.Scope.EnvironmentID)
	if err != nil || len(channels) == 0 {
		return err
	}
	where := flagnotify.Where{AccountID: env.Scope.AccountID, EnvironmentID: env.Scope.EnvironmentID, Project: env.ProjectSlug, Environment: env.EnvironmentSlug}
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
