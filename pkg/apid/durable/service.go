// Package durable is apid's durable entity preview: the HTTP handlers, the
// alarm/outbox/maintenance/health/backup workers, operator configuration,
// validator release gates and metrics built on pkg/durableentity (ADR-712,
// ADR-933..948). It was lifted out of cmd/apid by ADR-977.
//
// The package never sees apid's *server. Everything it needs from apid goes
// through Host, which cmd/apid implements by reading its own fields at call
// time. Service is a plain value so a zero apid server still has a usable,
// disabled preview.
package durable

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what the durable entity preview needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	Logger() *slog.Logger
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	// ResolveQueueEnvironment maps a requested environment to its deployment
	// scope and, for project apps, the catalog environment.
	ResolveQueueEnvironment(ctx context.Context, acct state.Account, app state.App, requested string) (string, *state.ProjectEnvironment, *api.Problem)
	// EnqueuePreparedInvocation admits and queues a fully resolved invocation.
	EnqueuePreparedInvocation(ctx context.Context, inv state.Invocation, capacityDetail string) (state.Invocation, *api.Problem)
	// ResultRetention is how long an invocation result is kept for acct.
	ResultRetention(acct state.Account) *time.Time
	// SecretRecipient seals isolated validator payloads; nil when unavailable.
	SecretRecipient() *age.X25519Recipient
	ExecutionAPIEnabled() bool
}

// Service is the durable entity preview. Configure fills the engine and
// operator gates at boot; tests set the exported fields directly.
type Service struct {
	registryHealth               registryCleanupHealth
	Host                         Host
	Sockets                      *entitysocket.Hub
	SessionGrants                *entitysocket.Grants
	TenantQuotas                 *entitysocket.Quotas
	QuotaMigrationEnabled        bool
	QuotaPolicyEnabled           bool
	RetirementEnabled            bool
	RegistryEnabled              bool
	RegistryCleanupEnabled       bool
	RegistryCleanupWorkerEnabled bool

	Engine *durableentity.Manager
	// Owner is this replica's lease owner identity.
	Owner string
	// Apps is the operator allowlist of app IDs admitted to the preview.
	Apps map[string]bool

	AlarmsEnabled               bool
	BackupsEnabled              bool
	HealthEnabled               bool
	MaintenanceEnabled          bool
	OutboxEnabled               bool
	OutboxHandlersEnabled       bool
	RestoreValidationEnabled    bool
	RestoreIsolationEnabled     bool
	ValidatorReleaseGateEnabled bool

	// ValidatorBundles is the file registry used when ValidatorArtifacts
	// (shared object storage) is not configured.
	ValidatorBundles   map[string]validatorbundle.Bundle
	ValidatorArtifacts *validatorbundle.Artifacts

	// Metrics is rebound by apid's WithOpsMetrics; nil disables observation.
	Metrics *Metrics
}

// executionAPIEnabled treats a Service without a Host as having no
// disposable execution runtime, so release gates fail closed.
func (s *Service) executionAPIEnabled() bool {
	return s.Host != nil && s.Host.ExecutionAPIEnabled()
}

// Registry is the registry the metrics were registered on, so apid can tell
// whether WithOpsMetrics needs to rebind them.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// registerCollector registers c, or returns the collector already registered
// under the same descriptor so rebinding to a registry reuses it.
func registerCollector[T prometheus.Collector](registry *prometheus.Registry, c T) T {
	err := registry.Register(c)
	if err == nil {
		return c
	}
	var alreadyRegistered prometheus.AlreadyRegisteredError
	if errors.As(err, &alreadyRegistered) {
		if existing, ok := alreadyRegistered.ExistingCollector.(T); ok {
			return existing
		}
	}
	panic(err)
}
