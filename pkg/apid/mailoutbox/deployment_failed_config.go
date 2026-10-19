package mailoutbox

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const DeploymentFailedTemplate = "deployment_failed"

func (s *Service) deploymentFailedEnabled() bool {
	return s.Enabled() && s.deploymentFailed != nil && s.deploymentFailed.enabled != nil && s.deploymentFailed.enabled()
}

// ConfigureDeploymentFailed reads only its own exact gate when disabled.
// The first successful enrollment sets a durable boundary; restarts reuse it.
func (s *Service) ConfigureDeploymentFailed(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.DeploymentFailedOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("deployment failure outbox requires configured producer outbox")
	}
	if err := s.queries.DeploymentFailedSchemaReady(ctx, s.pool); err != nil {
		return errors.New("deployment failure outbox schema unavailable")
	}
	ready, err := s.queries.DeploymentFailedReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("deployment failure outbox schema unavailable")
	}
	ready, err = s.queries.DeploymentFailedLifecycleReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("deployment failure lifecycle fence unavailable")
	}
	origin := getenv("FAAS_CLI_AUTH_URL_BASE")
	if origin == "" {
		origin = "https://gregale.dev"
	}
	origin, err = verificationOrigin(origin)
	if err != nil || len(origin) > api.InternalMailDeploymentFailedOriginMaxBytes {
		return errors.New("deployment failure dashboard origin unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("deployment failure encryption identities unavailable")
	}
	keys, err := s.queries.DeploymentFailedKeyReferences(ctx, s.pool, q.DeploymentFailedKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("deployment failure outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("deployment failure pending payload reader unavailable")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.DeploymentFailedOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	if err := s.enrollDeploymentCapture(ctx, on); err != nil {
		return err
	}
	s.deploymentDashboardOrigin = origin
	s.deploymentFailed = &credentialConfig{keyID: keyID, writer: writer, readers: readers, enabled: on,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithDeploymentFailedOutbox(on)}
	return nil
}

func (s *Service) enrollDeploymentCapture(ctx context.Context, on func() bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	if err := s.queries.EnrollDeploymentFailureCapture(ctx, tx, q.EnrollDeploymentFailureCaptureParams{Namespace: s.namespace, ProducerID: s.producer, RecoveryID: recovery.RecoveryID}); err != nil {
		return err
	}
	capture, err := s.queries.DeploymentFailureCapture(ctx, tx, q.DeploymentFailureCaptureParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return err
	}
	if capture.RecoveryID != recovery.RecoveryID {
		return internalmail.ErrRecoveryHeld
	}
	if !on() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
