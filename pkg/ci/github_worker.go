package ci

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RecordVerifiedGitHubDelivery belongs exclusively after the App HMAC check.
// It snapshots selected bindings at ingress, before acknowledging the webhook.
// Flag-off deliveries never enter the CI journal for later automatic replay.
func RecordVerifiedGitHubDelivery(ctx context.Context, cfg Config, store GitHubStore, deliveryID, kind string, body []byte) error {
	accounts := cfg.GitHubAccounts()
	if len(accounts) == 0 || store == nil {
		return nil
	}
	event, err := DecodeGitHubEvent(kind, body)
	if errors.Is(err, ErrGitHubIgnored) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if cfg.AutomationEnabled {
			capture, ok := store.(interface {
				RecordGitHubAutomationDelivery(context.Context, string, string, GitHubEvent) error
			})
			if !ok {
				return ErrGitHubUnavailable
			}
			if err := capture.RecordGitHubAutomationDelivery(ctx, account, deliveryID, event); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			continue
		}
		if err := store.RecordGitHubDelivery(ctx, account, deliveryID, event); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

// AdmitGitHubOnce is called only in apid: githubd records verified source
// evidence, while apid remains the sole writer of CI run intent.
func AdmitGitHubOnce(ctx context.Context, cfg Config, store GitHubStore, verify GitHubVerifier) (bool, error) {
	if store == nil || verify == nil {
		return false, nil
	}
	for _, account := range cfg.GitHubAccounts() {
		t, err := store.ClaimGitHubTrigger(ctx, account)
		if errors.Is(err, ErrNoGitHubWork) {
			continue
		}
		if err != nil {
			return false, err
		}
		workCtx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
		err = verify(workCtx, t.AccountID, t.Source.InstallationID, t.Source.RepositoryID, t.Source.Repository)
		cancel()
		if err == nil {
			_, err = store.AdmitGitHubTrigger(ctx, t)
		}
		if err == nil {
			return true, nil
		}
		reason := "admission_failed"
		if errors.Is(err, ErrGitHubAccess) {
			reason = "access_denied"
		}
		if errors.Is(err, ErrGitHubIgnored) {
			reason = "binding_disabled"
		}
		if errors.Is(err, ErrGitHubUnavailable) {
			reason = "unavailable"
		}
		saveErr := store.FailGitHubTrigger(ctx, t, reason)
		if reason == "access_denied" || reason == "binding_disabled" {
			return true, saveErr
		}
		return true, errors.Join(err, saveErr)
	}
	return false, nil
}

func PublishGitHubCheckOnce(ctx context.Context, cfg Config, store GitHubStore, verify GitHubVerifier, publish GitHubPublisher) (bool, error) {
	if !cfg.GitHubChecksEnabled || store == nil || verify == nil || publish == nil {
		return false, nil
	}
	for _, account := range cfg.GitHubAccounts() {
		check, err := store.ClaimGitHubCheck(ctx, account)
		if errors.Is(err, ErrNoGitHubWork) {
			continue
		}
		if errors.Is(err, ErrGitHubIgnored) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		workCtx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
		err = verify(workCtx, check.AccountID, check.Source.InstallationID, check.Source.RepositoryID, check.Source.Repository)
		id := int64(0)
		if err == nil {
			id, err = publish(workCtx, check)
		}
		cancel()
		if err == nil {
			return true, store.FinishGitHubCheck(ctx, check, id, "")
		}
		reason := "publish_failed"
		if errors.Is(err, ErrGitHubAccess) {
			reason = "access_denied"
		}
		if errors.Is(err, ErrGitHubUnavailable) {
			reason = "unavailable"
		}
		saveErr := store.FinishGitHubCheck(ctx, check, 0, reason)
		if reason == "access_denied" {
			return true, saveErr
		}
		return true, errors.Join(err, saveErr)
	}
	return false, nil
}

func RunGitHubAdmissionWorker(ctx context.Context, cfg Config, store GitHubStore, verify GitHubVerifier, log *slog.Logger) {
	if len(cfg.GitHubAccounts()) == 0 || store == nil || verify == nil {
		return
	}
	runGitHubWorker(ctx, func(ctx context.Context) (bool, error) { return AdmitGitHubOnce(ctx, cfg, store, verify) }, log)
}

func RunGitHubCheckWorker(ctx context.Context, cfg Config, store GitHubStore, verify GitHubVerifier, publish GitHubPublisher, log *slog.Logger) {
	if !cfg.GitHubChecksEnabled || len(cfg.GitHubAccounts()) == 0 || store == nil || verify == nil || publish == nil {
		return
	}
	runGitHubWorker(ctx, func(ctx context.Context) (bool, error) {
		return PublishGitHubCheckOnce(ctx, cfg, store, verify, publish)
	}, log)
}

func runGitHubWorker(ctx context.Context, once func(context.Context) (bool, error), log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(api.CIGitHubPollSeconds * time.Second)
	defer ticker.Stop()
	for {
		for range api.CIGitHubWorkerBatch {
			worked, err := once(ctx)
			recordWorkerCycle(ctx, err)
			if err != nil {
				log.Warn("internal CI GitHub reconciliation failed", "err", err)
				break
			}
			if !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
