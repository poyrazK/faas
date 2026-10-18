// Package livequeries is apid's half of ADR-20261016151947 live queries: it enables
// them per managed database and issues, rotates and revokes the database's
// live secret. Secrets are sealed to livequeryd's age recipient before they
// are stored, so apid cannot read one back; the customer sees each secret
// once, in the response that issues it.
package livequeries

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

const docsURL = "https://gregale.dev/docs/managed-postgres#live-queries"

// Databases is the managed Postgres service as live queries use it.
type Databases interface {
	Get(ctx context.Context, accountID, databaseID string) (managedpostgres.Database, error)
	LiveHub(managedpostgres.Database) (string, error)
}

// Keys stores live secrets; *livekeys.Store implements it.
type Keys interface {
	Enable(ctx context.Context, accountID, databaseID string, key livekeys.NewKey) error
	Rotate(ctx context.Context, accountID, databaseID string, key livekeys.NewKey, overlap time.Duration) error
	Disable(ctx context.Context, accountID, databaseID string) (bool, error)
	Keys(ctx context.Context, accountID, databaseID string) ([]livekeys.Key, error)
}

// Service serves /v1/postgres/databases/{id}/live.
type Service struct {
	Databases Databases
	Keys      Keys
	// Recipient is livequeryd's age recipient. Without it live queries are
	// unavailable.
	Recipient *age.X25519Recipient
	// DatabaseProblem writes a managed Postgres error.
	DatabaseProblem func(http.ResponseWriter, error)
	Audit           func(ctx context.Context, action string, accountID string, fields map[string]any)
	// Changed, when set, runs after a database's keys change, so realtime
	// endpoints learn the new key IDs without waiting for reconciliation.
	Changed func(ctx context.Context, accountID, databaseID string)
	Log     *slog.Logger
}

// Get reports a database's live-query state.
func (s *Service) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.database(w, r, acct)
	if !ok {
		return
	}
	s.writeState(w, r, acct, db.ID, "", http.StatusOK)
}

// Enable turns live queries on and issues the database's first secret.
func (s *Service) Enable(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.database(w, r, acct)
	if !ok || !s.supported(w, db) {
		return
	}
	secret, key, ok := s.newKey(w, r)
	if !ok {
		return
	}
	err := s.Keys.Enable(r.Context(), acct.ID, db.ID, key)
	if errors.Is(err, livekeys.ErrEnabled) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "live_queries_enabled", "Live queries are already enabled",
			"this database already has a live secret; rotate it to issue a new one").WithFix("gregale postgres live rotate "+db.Name).WithDocs(docsURL))
		return
	}
	if !s.stored(w, r, err) {
		return
	}
	s.changed(r.Context(), acct, db.ID, "managed_postgres.live.enabled", key.KID)
	s.writeState(w, r, acct, db.ID, secret, http.StatusCreated)
}

// Rotate issues a new secret; the previous one retires after the overlap.
func (s *Service) Rotate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.RotateManagedPostgresLiveSecretRequest
	if err := httpkit.DecodeJSONSized(r, &req, 1<<10); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, invalid("the request body must be JSON: {\"overlap_seconds\": <seconds>}"))
		return
	}
	overlap, problem := rotationOverlap(req.OverlapSeconds)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	db, ok := s.database(w, r, acct)
	if !ok {
		return
	}
	secret, key, ok := s.newKey(w, r)
	if !ok {
		return
	}
	err := s.Keys.Rotate(r.Context(), acct.ID, db.ID, key, overlap)
	if errors.Is(err, livekeys.ErrDisabled) {
		api.WriteProblem(w, notEnabled(db.Name))
		return
	}
	if !s.stored(w, r, err) {
		return
	}
	s.changed(r.Context(), acct, db.ID, "managed_postgres.live.rotated", key.KID)
	s.writeState(w, r, acct, db.ID, secret, http.StatusOK)
}

// Disable revokes every secret of the database and turns live queries off.
// It is idempotent.
func (s *Service) Disable(w http.ResponseWriter, r *http.Request, acct state.Account) {
	db, ok := s.database(w, r, acct)
	if !ok {
		return
	}
	removed, err := s.Keys.Disable(r.Context(), acct.ID, db.ID)
	if !s.stored(w, r, err) {
		return
	}
	if removed {
		s.changed(r.Context(), acct, db.ID, "managed_postgres.live.disabled", "")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) database(w http.ResponseWriter, r *http.Request, acct state.Account) (managedpostgres.Database, bool) {
	if s == nil || s.Databases == nil || s.Keys == nil {
		api.WriteProblem(w, unavailable())
		return managedpostgres.Database{}, false
	}
	db, err := s.Databases.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		s.DatabaseProblem(w, err)
		return managedpostgres.Database{}, false
	}
	return db, true
}

// supported reports whether db can run live queries now.
func (s *Service) supported(w http.ResponseWriter, db managedpostgres.Database) bool {
	hub, err := s.Databases.LiveHub(db)
	if err != nil {
		s.DatabaseProblem(w, err)
		return false
	}
	if hub == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "live_queries_unsupported", "Live queries are not available for this database",
			"live queries run on native Gregale Postgres databases; this database's backend does not run them").WithDocs(docsURL))
		return false
	}
	if db.State != managedpostgres.StateReady && db.State != managedpostgres.StateUpdating {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "live_queries_database_not_ready", "The database is not ready",
			fmt.Sprintf("the database is %s; enable live queries once it is ready", db.State)))
		return false
	}
	return true
}

// newKey generates a secret and seals it for livequeryd.
func (s *Service) newKey(w http.ResponseWriter, r *http.Request) (string, livekeys.NewKey, bool) {
	if s.Recipient == nil {
		api.WriteProblem(w, unavailable())
		return "", livekeys.NewKey{}, false
	}
	secret, err := seal.GenerateSecret()
	if err == nil {
		var parsed seal.Secret
		if parsed, err = seal.ParseSecret(secret); err == nil {
			var sealed []byte
			if sealed, err = secretbox.SealBytes(s.Recipient, livekeys.SealNamespace, []byte(secret), 256); err == nil {
				return secret, livekeys.NewKey{KID: parsed.KID, Sealed: sealed, Recipient: s.Recipient.String()}, true
			}
		}
	}
	httpkit.WriteCustomerInternalProblem(w, r, s.Log, "live secret issue", "The live secret could not be issued.", "retry the request", err)
	return "", livekeys.NewKey{}, false
}

func (s *Service) stored(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, livekeys.ErrNotFound):
		s.DatabaseProblem(w, managedpostgres.ErrNotFound)
	default:
		httpkit.WriteCustomerInternalProblem(w, r, s.Log, "live secret store", "The live-query change could not be saved.", "retry the request", err)
	}
	return false
}

func (s *Service) changed(ctx context.Context, acct state.Account, databaseID, action, kid string) {
	if s.Audit != nil {
		fields := map[string]any{"database_id": databaseID}
		if kid != "" {
			fields["key_id"] = kid
		}
		s.Audit(ctx, action, acct.ID, fields)
	}
	if s.Changed != nil {
		s.Changed(ctx, acct.ID, databaseID)
	}
}

func (s *Service) writeState(w http.ResponseWriter, r *http.Request, acct state.Account, databaseID, secret string, status int) {
	keys, err := s.Keys.Keys(r.Context(), acct.ID, databaseID)
	if err != nil {
		httpkit.WriteCustomerInternalProblem(w, r, s.Log, "live secret list", "The live-query state could not be read.", "retry the request", err)
		return
	}
	out := api.ManagedPostgresLiveQueries{DatabaseID: databaseID, Keys: make([]api.ManagedPostgresLiveKey, 0, len(keys)), Secret: secret}
	for _, k := range keys {
		view := api.ManagedPostgresLiveKey{KeyID: k.KID, Active: k.RetiresAt.IsZero(), CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339)}
		if !k.RetiresAt.IsZero() {
			view.RetiresAt = k.RetiresAt.UTC().Format(time.RFC3339)
		}
		out.Enabled = out.Enabled || view.Active
		out.Keys = append(out.Keys, view)
	}
	// The secret must not be cached or forwarded.
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, status, out)
}

func rotationOverlap(seconds *int64) (time.Duration, *api.Problem) {
	if seconds == nil {
		return api.LiveQuerySecretRotationOverlap, nil
	}
	maxSeconds := int64(api.LiveQuerySecretRotationOverlapMax / time.Second)
	if *seconds < 0 || *seconds > maxSeconds {
		return 0, invalid(fmt.Sprintf("overlap_seconds must be between 0 and %d", maxSeconds)).WithLimit(maxSeconds, *seconds).WithDocs(docsURL)
	}
	return time.Duration(*seconds) * time.Second, nil
}

func invalid(detail string) *api.Problem {
	return api.NewProblem(http.StatusBadRequest, "live_queries_invalid", "Invalid live-query request", detail)
}

func notEnabled(database string) *api.Problem {
	return api.NewProblem(http.StatusConflict, "live_queries_not_enabled", "Live queries are not enabled",
		"this database has no live secret to rotate").WithFix("gregale postgres live enable " + database).WithDocs(docsURL)
}

func unavailable() *api.Problem {
	return api.NewProblem(http.StatusServiceUnavailable, "live_queries_unavailable", "Live queries are unavailable",
		"live queries are an operator preview and are not configured in this environment").WithDocs(docsURL)
}
