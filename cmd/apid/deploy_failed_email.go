package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// deploymentFailedEmailPayload is the small trigger payload emitted by the
// deployments_failed_notify trigger. The row remains authoritative; these
// fields are only a wake-up hint and are intentionally optional for rolling
// upgrades and manually emitted notifications.
type deploymentFailedEmailPayload struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
	To           string `json:"to"`
	Status       string `json:"status"`
}

// runDeployFailedEmailSubscriber bridges the transactional deployment-failure
// notification into the outbound email sender. It follows the same
// reconnecting LISTEN lifecycle as the audit and OpenAPI subscribers.
func runDeployFailedEmailSubscriber(ctx context.Context, pool *pgxpool.Pool, srv *server, log *slog.Logger) error {
	ch, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyDeploymentChanged}, log)
	if err != nil {
		return err
	}
	log.Info("deploy_failed_email: subscriber started", "channel", db.NotifyDeploymentChanged)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				return nil
			}
			if n.Payload == "" {
				continue
			}
			var p deploymentFailedEmailPayload
			if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
				log.Warn("deploy_failed_email: bad payload", "err", err)
				continue
			}
			if p.Status != "" && p.Status != string(state.DeployFailed) {
				continue
			}
			if err := sendDeployFailedEmail(ctx, srv, p); err != nil && !errors.Is(err, state.ErrNotFound) {
				log.Warn("deploy_failed_email: delivery skipped", "deployment_id", logsanitize.Field(p.DeploymentID), "err", err)
			}
		}
	}
}

func sendDeployFailedEmail(parent context.Context, srv *server, p deploymentFailedEmailPayload) error {
	if srv == nil || srv.store == nil || srv.mailer == nil {
		return nil
	}
	deploymentID := p.DeploymentID
	if deploymentID == "" {
		deploymentID = p.To
	}
	if deploymentID == "" {
		return fmt.Errorf("deployment id missing")
	}
	dep, err := srv.store.DeploymentByID(parent, deploymentID)
	if err != nil {
		return err
	}
	if dep.Status != state.DeployFailed {
		return nil
	}
	appID := dep.AppID
	if p.AppID != "" && p.AppID != dep.AppID {
		// The payload is only a hint. Never cross the deployment's durable
		// app boundary if a stale or malformed notification disagrees.
		return nil
	}
	app, err := srv.store.AppByID(parent, appID)
	if err != nil {
		return err
	}
	accountID := dep.DeployedByUserID
	if accountID == "" {
		accountID = app.AccountID
	}
	acct, err := srv.store.AccountByID(parent, accountID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(acct.Email) == "" {
		return nil
	}
	cooldown, ok := srv.store.(state.DeployFailedEmailCooldownStore)
	if !ok {
		return fmt.Errorf("store does not implement deploy-failed email cooldown")
	}
	now := time.Now().UTC()
	claimed, err := cooldown.ClaimDeployFailedEmail(parent, app.ID, now)
	if err != nil || !claimed {
		return err
	}

	subject, body := mail.DeploymentFailedBody(mail.DeploymentFailure{
		AppSlug:      app.Slug,
		DeploymentID: dep.ID,
		ErrorCode:    dep.ErrorCode,
		Error:        dep.Error,
		ErrorHint:    dep.ErrorHint,
		ErrorWhy:     dep.ErrorWhy,
		ErrorFix:     dep.ErrorFix,
		RelevantLogs: dep.ErrorRelevantLogs,
		DashboardURL: deploymentDashboardURL(srv.cliAuthURLBase, app.Slug, dep.ID),
		FailedAt:     now,
	})
	mailCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	if err := srv.mailer.Send(mailCtx, Message{
		To:        []string{acct.Email},
		Subject:   subject,
		TextBody:  body,
		MessageID: "deploy_failed:" + dep.ID,
	}); err != nil {
		return err
	}
	return nil
}

func deploymentDashboardURL(base, slug, deploymentID string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://gregale.dev"
	}
	return base + "/dashboard/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deploymentID)
}
