package internalmail

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	outboxq "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
)

var errDeploymentFailedObsolete = errors.New("deployment failure notification no longer applies")

type deploymentFailedSubmissionGateKey struct{}
type deploymentFailedSubmissionStore interface {
	shareDeploymentFailedSource(context.Context, pgx.Tx, Recipient) error
}

func (s *PGStore) DeploymentFailedOutboxReady(ctx context.Context) error {
	if err := outboxq.New().DeploymentFailedSchemaReady(ctx, s.pool); err != nil {
		return err
	}
	ready, err := outboxq.New().DeploymentFailedReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return ErrInvalid
	}
	ready, err = outboxq.New().DeploymentFailedLifecycleReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return ErrInvalid
	}
	return nil
}

func deploymentFailedMessage(m Message) bool {
	return m.ProducerID != "" && strings.HasPrefix(m.IdempotencyKey, "deployment-failed:")
}
func (s *Service) deploymentFailedSubmissionContext(ctx context.Context, m Message) (context.Context, error) {
	if !deploymentFailedMessage(m) {
		return ctx, nil
	}
	if !s.deploymentFailedOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if _, ok := s.Store.(deploymentFailedSubmissionStore); !ok {
		return ctx, ErrInvalid
	}
	return context.WithValue(ctx, deploymentFailedSubmissionGateKey{}, s.deploymentFailedOutboxEnabled), nil
}

// These metadata locks span maild's durable SMTP-start commit. Source writers
// use the scope fence without an app/account update lock in their trigger.
// An already committed submission start cannot be recalled by a later change.
func (s *PGStore) shareDeploymentFailedSource(ctx context.Context, tx pgx.Tx, r Recipient) error {
	q := outboxq.New()
	row, err := q.ShareDeploymentFailureSubmissionAccount(ctx, tx, outboxq.ShareDeploymentFailureSubmissionAccountParams{Namespace: s.namespace, ID: pgID(r.MessageID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return errDeploymentFailedObsolete
	}
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(row.FromAddress)
	if err != nil || !strings.EqualFold(from.Address, row.SenderAddress) {
		return errDeploymentFailedObsolete
	}
	app, err := q.ShareDeploymentFailureApp(ctx, tx, row.DeploymentFailureAppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errDeploymentFailedObsolete
	}
	if err != nil {
		return err
	}
	if app.AccountID != row.AccountID {
		if !app.OrgID.Valid {
			return errDeploymentFailedObsolete
		}
		_, err = q.ShareDeploymentFailureMember(ctx, tx, outboxq.ShareDeploymentFailureMemberParams{OrgID: app.OrgID, AccountID: row.AccountID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errDeploymentFailedObsolete
		}
		if err != nil {
			return err
		}
	}
	_, err = q.ShareDeploymentFailureFence(ctx, tx, outboxq.ShareDeploymentFailureFenceParams{AppID: app.ID, Scope: row.DeploymentFailureScope.String})
	if errors.Is(err, pgx.ErrNoRows) {
		return errDeploymentFailedObsolete
	}
	if err != nil {
		return err
	}
	current, err := q.DeploymentFailureCurrent(ctx, tx, outboxq.DeploymentFailureCurrentParams{Namespace: s.namespace, ID: row.IntentID, RecipientAddress: r.Address})
	if err != nil {
		return err
	}
	if !current {
		return errDeploymentFailedObsolete
	}
	return nil
}
