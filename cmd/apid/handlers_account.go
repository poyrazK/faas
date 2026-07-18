package main

// G6 account self-service handlers (spec §17 G6, ADR-021).
//
// Each handler is small enough to read at a glance and delegates the
// bulk of the work to helpers in the same file. The handlers sit
// behind s.auth with the deleted_pending carve-out applied at the
// middleware layer (see cmd/apid/server.go::auth + isAccountScopedPath).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// exportAccount writes a single JSON bundle of every row tied to the
// account. ?include_secrets=false drops the ciphertext slice (the
// default is to include — see ADR-021 D4).
func (s *server) exportAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	include := r.URL.Query().Get("include_secrets") != "false"
	bundle, err := gatherExport(r.Context(), s, acct, include)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not assemble export"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		`attachment; filename="faas-account-`+acct.ID+`-`+
			time.Now().UTC().Format("20060102")+`.json"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(bundle)
}

// deleteAccount schedules the account for hard delete in 30 days.
// Idempotent: a second DELETE while already in deleted_pending
// returns the same envelope (status + scheduled_at + restore_until)
// without re-stamping the timestamp or re-sending the email.
func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	fresh, err := s.scheduleDeletion(r.Context(), acct)
	if err != nil {
		api.WriteProblem(w, err)
		return
	}
	writeDeletionEnvelope(w, fresh)
}

// scheduleDeletion is the business-logic core reused by both the
// REST handler (deleteAccount) and the dashboard form handler
// (dashboardDelete in handlers_dashboard.go). Idempotent: a repeat
// call on a row already in deleted_pending returns the existing
// envelope without re-sending the email.
func (s *server) scheduleDeletion(ctx context.Context, acct state.Account) (state.Account, *api.Problem) {
	if acct.Status != state.AccountDeletedPending {
		if err := s.store.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
			return acct, api.ErrCapacity("could not mark for deletion")
		}
		fresh, err := s.store.AccountByID(ctx, acct.ID)
		if err != nil {
			return acct, api.ErrCapacity("could not refresh account")
		}
		if fresh.DeletionRequestedAt != nil {
			restoreUntil := fresh.DeletionRequestedAt.Add(state.DeletionGraceDuration())
			subject, body := mail.AccountDeletionPendingBody(
				fresh.Email, *fresh.DeletionRequestedAt, restoreUntil)
			_ = s.mailer.Send(ctx, Message{
				To: []string{fresh.Email}, Subject: subject, TextBody: body,
			})
		}
		return fresh, nil
	}
	return acct, nil
}

// restoreAccount flips the account back to active iff inside the
// 30-day grace window. Past grace → 409 account_not_restorable.
func (s *server) restoreAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	fresh, prob := s.cancelDeletion(r.Context(), acct)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), fresh, r))
}

// cancelDeletion is the business-logic core reused by both the REST
// handler (restoreAccount) and the dashboard form handler. Returns
// (refreshed-account, problem). A nil problem means success.
func (s *server) cancelDeletion(ctx context.Context, acct state.Account) (state.Account, *api.Problem) {
	if acct.Status != state.AccountDeletedPending {
		return acct, api.NewProblem(http.StatusConflict, api.CodeAccountNotRestorable,
			"Not restorable",
			"account is not in the deletion grace window")
	}
	if err := s.store.RestoreAccount(ctx, acct.ID); err != nil {
		return acct, api.NewProblem(http.StatusConflict, api.CodeAccountNotRestorable,
			"Grace expired",
			"the 30-day grace window has lapsed; restore is no longer possible")
	}
	fresh, err := s.store.AccountByID(ctx, acct.ID)
	if err != nil {
		return acct, api.ErrCapacity("could not refresh account")
	}
	return fresh, nil
}

// dpaTemplate serves the DPA plaintext template. No auth — the DPA is
// a public artefact a prospect reads before signing up (spec §17 G6).
// 503 when the path is unset (production box without the file) so a
// misconfigured deploy is observable instead of silently empty.
func (s *server) dpaTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dpaPath == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable,
			api.CodeCapacity, "DPA template unavailable",
			"FAAS_DPA_PATH is unset; contact support@DOMAIN for the DPA"))
		return
	}
	body, err := os.ReadFile(s.dpaPath)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("DPA template unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writeDeletionEnvelope emits the 200 body for both the initial
// DELETE and every idempotent retry. RFC 3339 timestamps so the
// dashboard and the CLI can render the deadline uniformly.
func writeDeletionEnvelope(w http.ResponseWriter, acct state.Account) {
	resp := api.AccountDeletionResponse{Status: string(acct.Status)}
	if acct.DeletionRequestedAt != nil {
		resp.ScheduledAt = acct.DeletionRequestedAt.UTC().Format(time.RFC3339)
		resp.RestoreUntil = acct.DeletionRequestedAt.Add(state.DeletionGraceDuration()).
			UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// gatherExport walks every per-resource list inside one sequence of
// store calls. The slice order is the order the bundle serializes —
// top-level fields first so reviewers can see the envelope shape at
// a glance.
func gatherExport(ctx context.Context, s *server, acct state.Account, includeSecrets bool) (api.AccountExportResponse, error) {
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		return api.AccountExportResponse{}, err
	}
	appOut := make([]api.AppResponse, 0, len(apps))
	for _, a := range apps {
		appOut = append(appOut, s.appResponse(a))
	}
	return api.AccountExportResponse{
		ExportedAt:  time.Now().UTC().Format(time.RFC3339),
		// No incoming request context here — the export is built
		// outside any handler scope (the inner per-resource helpers
		// already carry the request ctx); accountResponse's third
		// argument is nil so the "skip AppCount/Usage lookups" branch
		// fires regardless.
		//nolint:contextcheck
		Account: s.accountResponse(context.Background(), acct, nil),
		Apps:        appOut,
		Deployments: listDeploymentsForAccountExport(ctx, s.store, acct.ID),
		Builds:      listBuildsForAccountExport(ctx, s.store, acct.ID),
		Instances:   listInstancesForAccountExport(ctx, s.store, acct.ID),
		Usage:       listUsageForAccountExport(ctx, s.store, acct.ID),
		Domains:     listDomainsForAccountExport(ctx, s.store, acct.ID),
		Crons:       listCronsForAccountExport(ctx, s.store, acct.ID),
		APIKeys:     listKeysForAccountExport(ctx, s.store, acct.ID),
		AppSecrets:  listSecretsForAccountExport(ctx, s.store, acct.ID, apps, includeSecrets),
	}, nil
}

// --- per-resource list helpers (each ≤50 LoC, exported for tests) -------

func listDeploymentsForAccountExport(ctx context.Context, st state.Store, accountID string) []api.DeploymentResponse {
	rows, err := st.ListDeploymentsForAccount(ctx, accountID, time.Time{}, 1000)
	if err != nil {
		return nil
	}
	out := make([]api.DeploymentResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, api.DeploymentResponse{
			ID: d.ID, AppID: d.AppID, BuildID: d.BuildID,
			ImageDigest: d.ImageDigest, Kind: string(d.Kind),
			Status: string(d.Status), Error: sanitizeExportString(d.Error),
			CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func listBuildsForAccountExport(ctx context.Context, st state.Store, accountID string) []api.BuildExportResponse {
	rows, err := st.ListBuildsForAccount(ctx, accountID)
	if err != nil {
		return nil
	}
	out := make([]api.BuildExportResponse, 0, len(rows))
	for _, b := range rows {
		out = append(out, api.BuildExportResponse{
			ID: b.ID, DeploymentID: b.DeploymentID, Kind: string(b.Kind),
			Status: string(b.Status), SourceBytes: b.SourceBytes,
			StartedAt:  b.StartedAt.UTC().Format(time.RFC3339),
			FinishedAt: b.FinishedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func listInstancesForAccountExport(ctx context.Context, st state.Store, accountID string) []api.InstanceResponse {
	rows, err := st.ListInstancesForAccount(ctx, accountID)
	if err != nil {
		return nil
	}
	out := make([]api.InstanceResponse, 0, len(rows))
	for _, ins := range rows {
		out = append(out, api.InstanceResponse{
			ID: ins.ID, AppID: ins.AppID, DeploymentID: ins.DeploymentID,
			State: ins.State, HostIP: ins.HostIP, RAMMB: ins.RAMMB,
			StartedAt:     ins.StartedAt.UTC().Format(time.RFC3339),
			LastRequestAt: ins.LastRequestAt.UTC().Format(time.RFC3339),
			ParkedAt:      ins.ParkedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func listUsageForAccountExport(ctx context.Context, st state.Store, accountID string) []api.UsageExportResponse {
	rows, err := st.UsageByAccount(ctx, accountID, time.Time{})
	if err != nil {
		return nil
	}
	out := make([]api.UsageExportResponse, 0, len(rows))
	for _, u := range rows {
		out = append(out, api.UsageExportResponse{
			AppID: u.AppID, Month: u.Month.UTC().Format("2006-01"),
			MBSeconds: u.MBSeconds, Requests: u.Requests,
		})
	}
	return out
}

func listDomainsForAccountExport(ctx context.Context, st state.Store, accountID string) []api.CustomDomainResponse {
	rows, err := st.ListDomainsForAccount(ctx, accountID)
	if err != nil {
		return nil
	}
	out := make([]api.CustomDomainResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, api.CustomDomainResponse{
			Domain:     d.Domain,
			AppID:      d.AppID,
			Verified:   d.Verified(),
			VerifiedAt: formatTimeOrEmpty(d.VerifiedAt),
		})
	}
	return out
}

func listCronsForAccountExport(ctx context.Context, st state.Store, accountID string) []api.CronResponse {
	rows, err := st.ListCronsForAccount(ctx, accountID)
	if err != nil {
		return nil
	}
	out := make([]api.CronResponse, 0, len(rows))
	for _, c := range rows {
		out = append(out, api.CronResponse{
			ID: c.ID, AppID: c.AppID, Schedule: c.Schedule,
			Path: c.Path, Enabled: c.Enabled,
			CreatedAt:   c.CreatedAt.UTC().Format(time.RFC3339),
			LastFiredAt: formatTimeOrEmpty(c.LastFiredAt),
		})
	}
	return out
}

func listKeysForAccountExport(ctx context.Context, st state.Store, accountID string) []api.APIKeyExportResponse {
	rows, err := st.ListAPIKeys(ctx, accountID)
	if err != nil {
		return nil
	}
	out := make([]api.APIKeyExportResponse, 0, len(rows))
	for _, k := range rows {
		out = append(out, api.APIKeyExportResponse{
			ID:        k.ID,
			Prefix:    prefixFromHash(k.Hash),
			Label:     k.Label,
			CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
			LastUsed:  formatTimeOrEmpty(k.LastUsedAt),
		})
	}
	return out
}

// listSecretsForAccountExport walks every app on the account and
// aggregates the per-app ciphertext rows. When include is false (the
// caller passed ?include_secrets=false) we drop the slice entirely
// so the customer can fetch an export without revealing ciphertext
// to a backup they don't control.
func listSecretsForAccountExport(ctx context.Context, st state.Store, accountID string, apps []state.App, include bool) []api.AppSecretExportResponse {
	if !include {
		return nil
	}
	var out []api.AppSecretExportResponse
	for _, a := range apps {
		rows, err := st.ListAppSecrets(ctx, accountID, a.ID)
		if err != nil {
			continue
		}
		for _, sec := range rows {
			out = append(out, api.AppSecretExportResponse{
				AppID:      sec.AppID,
				Key:        sec.Key,
				Ciphertext: base64.RawURLEncoding.EncodeToString(sec.Ciphertext),
				CreatedAt:  sec.CreatedAt.UTC().Format(time.RFC3339),
				UpdatedAt:  sec.UpdatedAt.UTC().Format(time.RFC3339),
			})
		}
	}
	return out
}

// formatTimeOrEmpty renders t as RFC 3339 in UTC, or "" if zero. Used
// for nullable timestamp columns (verified_at, last_fired_at,
// last_used_at) so the export's wire shape stays a single source of
// truth instead of every helper re-deriving the empty-string rule.
func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// prefixFromHash returns the first 8 bytes of an API key hash hex-
// encoded — matches the prefix the GET /v1/keys surface renders.
// Plaintext is never available here (MemStore and PgStore only
// store hashes), so the prefix is the most honest identifier the
// export can carry.
func prefixFromHash(hash []byte) string {
	if len(hash) == 0 {
		return ""
	}
	const width = 8
	if len(hash) < width {
		return base64.RawURLEncoding.EncodeToString(hash)
	}
	const hexchars = "0123456789abcdef"
	out := make([]byte, 0, width*2)
	for i := 0; i < width; i++ {
		b := hash[i]
		out = append(out, hexchars[b>>4], hexchars[b&0x0f])
	}
	return string(out)
}

// sanitizeExportString strips control characters from a string before it
// lands in the GDPR export bundle. Today the only such field is
// Deployment.Error; the field is opaque to apid (set by imaged / schedd)
// so a future maintainer could unwittingly stash a path or token. This
// is a defence-in-depth pass — preserves printable content, drops
// anything < 0x20 except \t and \n.
func sanitizeExportString(s string) string {
	if s == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}