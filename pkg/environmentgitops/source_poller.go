package environmentgitops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type SourceReader interface {
	ReadEnvironmentGitSource(context.Context, state.EnvironmentGitSource) (state.EnvironmentGitSourcePollResult, error)
}

// SourcePoller reads candidates independently of approved intent execution.
// Reader errors are never persisted or logged verbatim: a Git transport error
// may contain installation credentials or customer definition contents.
type SourcePoller struct {
	Store         state.EnvironmentGitSourcePollStore
	Reader        SourceReader
	Log           *slog.Logger
	Now           func() time.Time
	LeaseDuration time.Duration
	ReadTimeout   time.Duration
	CheckInterval time.Duration
	RetryInterval time.Duration
}

func (p *SourcePoller) clock() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *SourcePoller) validate() error {
	if p.Store == nil || p.Reader == nil || p.ReadTimeout <= 0 || p.ReadTimeout >= p.LeaseDuration || p.CheckInterval <= 0 || p.RetryInterval <= 0 {
		return fmt.Errorf("environment Git source poller requires store, reader, and bounded positive durations")
	}
	return nil
}

func (p *SourcePoller) RunOnce(ctx context.Context) (bool, error) {
	if err := p.validate(); err != nil {
		return false, err
	}
	lease, err := p.Store.ClaimEnvironmentGitSourcePoll(ctx, uuid.NewString(), p.clock(), p.LeaseDuration)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	readCtx, cancel := context.WithTimeout(ctx, p.ReadTimeout)
	result, err := p.Reader.ReadEnvironmentGitSource(readCtx, lease.Source)
	cancel()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		result = state.EnvironmentGitSourcePollResult{ErrorCode: "environment_git_source_unavailable"}
	}
	interval := p.CheckInterval
	if result.ErrorCode != "" {
		interval = p.RetryInterval
	}
	now := p.clock()
	err = p.Store.FinishEnvironmentGitSourcePoll(ctx, lease, result, now, now.Add(interval))
	if errors.Is(err, state.ErrConflict) {
		return true, nil // a newer source generation or claim owns the result
	}
	return true, err
}

func (p *SourcePoller) Run(ctx context.Context, idleInterval time.Duration) error {
	if err := p.validate(); err != nil || idleInterval <= 0 {
		return fmt.Errorf("invalid environment Git source poller configuration")
	}
	ticker := time.NewTicker(idleInterval)
	defer ticker.Stop()
	for {
		worked, err := p.RunOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && p.Log != nil {
			p.Log.Warn("environment Git source poll could not complete", "error_code", "environment_git_source_poll_failed")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
