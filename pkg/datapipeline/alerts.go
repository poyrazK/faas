package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// AlertSettings and AlertDelivery are safe dashboard projections: neither the
// endpoint (which may contain a credential) nor the signing key leaves storage.
type AlertSettings struct {
	Revision                 int64
	Enabled                  bool
	IncidentID, IncidentCode string
	History                  []AlertDelivery
}

type AlertDelivery struct {
	ID, IncidentID, Kind, Code, Status, Result, OccurredAt string
	Attempts, ResponseCode                                 int
}

type AlertStore interface {
	Alerts(context.Context, string, string) (AlertSettings, error)
	ConfigureAlerts(context.Context, string, string, int64, bool, []byte) error
}

type alertDestination struct{ URL, Secret string }

func validAlertDestination(d alertDestination) bool {
	if len(d.URL) > api.DataPipelineAlertURLMaxBytes || len(d.Secret) < api.DataPipelineAlertSecretMinBytes || len(d.Secret) > api.DataPipelineAlertSecretMaxBytes {
		return false
	}
	u, err := url.Parse(d.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(d.URL, "\r\n\x00") {
		return false
	}
	// This surface accepts DNS endpoints. The adapter resolves and enforces
	// the shared egress policy on every request, including retries.
	if u.Port() != "" && u.Port() != "443" {
		return false
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return false
	}
	return true
}

func alertNamespace(account, id string) string {
	// The service validates both IDs before sealing. PostgreSQL canonicalizes
	// UUID spellings, so the authenticated tag must use that same spelling.
	accountID, _ := uuid.Parse(account)
	pipelineID, _ := uuid.Parse(id)
	return "gregale.pipeline.alert." + accountID.String() + "." + pipelineID.String()
}

// ConfigureHealthAlerts replaces one opt-in destination. Replacement/disable
// cancels queued events for the previous destination; it does not replay them.
func (s *Service) ConfigureHealthAlerts(ctx context.Context, account, id string, revision int64, enabled bool, endpoint, secret string) error {
	if s == nil || s.Store == nil {
		return ErrUnavailable
	}
	if !validID(account) || !validID(id) || revision < 0 {
		return ErrInvalid
	}
	store, ok := s.Store.(AlertStore)
	if !ok {
		return ErrUnavailable
	}
	var ciphertext []byte
	if enabled {
		if err := s.admit(ctx, account); err != nil {
			return err
		}
		d := alertDestination{endpoint, secret}
		if !validAlertDestination(d) {
			return ErrInvalid
		}
		if s.Recipient == nil {
			return ErrUnavailable
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return ErrInvalid
		}
		ciphertext, err = secretbox.SealBytes(s.Recipient, alertNamespace(account, id), raw, api.DataPipelineCredentialMaxBytes)
		if err != nil {
			return ErrUnavailable
		}
	}
	return store.ConfigureAlerts(ctx, account, id, revision, enabled, ciphertext)
}

func (s *PostgresStore) Alerts(ctx context.Context, account, id string) (AlertSettings, error) {
	if !validID(account) || !validID(id) {
		return AlertSettings{}, ErrInvalid
	}
	if _, err := s.Get(ctx, account, id); err != nil {
		return AlertSettings{}, err
	}
	r, err := s.q.PipelineAlertGet(ctx, s.pool, sqlc.PipelineAlertGetParams{AccountID: account, PipelineID: id})
	if errors.Is(storeError(err), ErrNotFound) {
		return AlertSettings{}, nil
	}
	if err != nil {
		return AlertSettings{}, storeError(err)
	}
	a := AlertSettings{Revision: r.Revision, Enabled: r.Enabled, IncidentID: r.IncidentID, IncidentCode: r.IncidentCode}
	rows, err := s.q.PipelineAlertHistory(ctx, s.pool, sqlc.PipelineAlertHistoryParams{AccountID: account, PipelineID: id, PageLimit: api.DataPipelineAlertHistoryMax})
	if err != nil {
		return AlertSettings{}, storeError(err)
	}
	for _, d := range rows {
		a.History = append(a.History, AlertDelivery{ID: uid(d.ID), IncidentID: uid(d.IncidentID), Kind: d.Kind, Code: d.Code, Status: d.Status, Result: d.Result, OccurredAt: stamp(d.OccurredAt), Attempts: int(d.Attempts), ResponseCode: int(d.ResponseCode)})
	}
	return a, nil
}

func (s *PostgresStore) ConfigureAlerts(ctx context.Context, account, id string, revision int64, enabled bool, ciphertext []byte) error {
	if !validID(account) || !validID(id) || revision < 0 {
		return ErrInvalid
	}
	if ciphertext == nil {
		ciphertext = []byte{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = s.q.PipelineAlertConfigure(ctx, tx, sqlc.PipelineAlertConfigureParams{AccountID: account, PipelineID: id, ExpectedRevision: revision, Enabled: enabled, Ciphertext: ciphertext})
	if err != nil {
		if errors.Is(storeError(err), ErrNotFound) {
			return ErrConflict
		}
		return storeError(err)
	}
	if err = s.q.PipelineAlertCancelPending(ctx, tx, sqlc.PipelineAlertCancelPendingParams{PipelineID: id, Result: "destination_changed"}); err != nil {
		return storeError(err)
	}
	if err = s.q.PipelineAlertPrune(ctx, tx, sqlc.PipelineAlertPruneParams{PipelineID: id, HistoryOffset: api.DataPipelineAlertHistoryMax}); err != nil {
		return storeError(err)
	}
	return storeError(tx.Commit(ctx))
}

// alertCondition reports only catalog evidence. An old applied checkpoint on
// a quiet publication alone is not evidence that customer data is stale.
func alertCondition(p Pipeline, now time.Time) (code string, recovered bool) {
	if p.DesiredState == "deleted" || p.State == "deleted" {
		return "", false
	}
	observed, err := time.Parse(time.RFC3339Nano, p.ObservedAt)
	fresh := err == nil && !observed.After(now) && now.Sub(observed) < api.DataPipelineObservationMaxAge
	if p.DesiredState == "running" && !fresh {
		created, _ := time.Parse(time.RFC3339Nano, p.CreatedAt)
		if err != nil && now.Sub(created) < api.DataPipelineObservationMaxAge {
			return "", false
		}
		return "monitoring_stale", false
	}
	if !fresh {
		return "", false
	}
	if p.RetainedWALBytes != nil && *p.RetainedWALBytes >= api.DataPipelineRetentionWarningBytes {
		return "retention_pressure", false
	}
	if p.DesiredState != "running" || p.ObservedRevision != p.Revision {
		return "", false
	}
	if p.State == "blocked" || p.State == "resync_required" {
		return "sync_blocked", false
	}
	if (p.State == "live" || p.State == "catching_up") && p.RetainedWALBytes != nil && *p.RetainedWALBytes >= api.DataPipelineLiveLagMaxBytes {
		applied, err := time.Parse(time.RFC3339Nano, p.AppliedThroughAt)
		if err != nil || applied.After(now) || now.Sub(applied) >= api.DataPipelineObservationMaxAge {
			return "apply_unconfirmed", false
		}
	}
	return "", p.State == "live"
}
