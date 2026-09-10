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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// deletionPendingPayload is the JSON shape the account_deletion_pending
// pg_notify channel carries. Matches the contract documented at
// pkg/db/notify.go (account_id, scheduled_at, restore_until) so any
// future schedd subscriber can drop live instances at the moment of
// pending without re-deriving the grace window.
type deletionPendingPayload struct {
	AccountID    string `json:"account_id"`
	ScheduledAt  string `json:"scheduled_at"`
	RestoreUntil string `json:"restore_until"`
}

// exportAccount writes a single JSON bundle of every row tied to the
// account. includeSecretsFalse drops the ciphertext slice (the
// default is to include — see ADR-021 D4). Kept as a sentinel
// constant so goconst does not flag every "false" comparison site
// (the dashboard / REST endpoints all read the same query value).
const includeSecretsFalse = "false"

// exportAccount writes a single JSON bundle of every row tied to the
// account. ?include_secrets=false drops the ciphertext slice (the
// default is to include — see ADR-021 D4).
func (s *server) exportAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// Idempotency probe (issue #755 / PR-5.2). If the customer
	// supplied an X-Request-Id and a prior ledger row exists for
	// (account_id, request_id), the retry is logically the same
	// action as the original — skip the rate-limit and skip a fresh
	// ledger insert. We still re-build the bundle from the same
	// data sources so the customer sees any row-deltas since the
	// first call (the durable receipt is the ledger row; the bundle
	// is fully derived from the account's data).
	//
	// Empty inbound id falls through to the rate-limit path —
	// every existing customer keeps the same behaviour they had
	// before PR-5.2.
	requestID := middleware.RequestIDFrom(r)
	isIdempotentRetry := false
	if requestID != "" {
		prior, err := s.store.FindGdprRequestByRequestID(r.Context(), acct.ID, requestID)
		switch {
		case err == nil && prior.Action == state.GdprActionExport:
			isIdempotentRetry = true
			s.log.Info("apid: export request_id idempotent hit",
				"account", acct.ID, "request_id", requestID, "ledger_id", prior.ID)
		case err != nil && !errors.Is(err, state.ErrNotFound):
			// Probe failure is not a customer-visible problem — fall
			// through to the normal path. The worst case is the
			// customer gets a 429 on what would have been a retry,
			// which is the same behaviour as before PR-5.2.
			s.log.Warn("apid: export idempotency probe failed",
				"account", acct.ID, "request_id", requestID, "err", err)
		}
	}

	// 24h rate limit (issue #755 / PR-5.1). GDPR self-serve exports
	// are abusive under load — each bundle scans every per-account
	// table, so a single customer hitting the endpoint once a minute
	// for a day would cost more than the rest of the plan's quota
	// combined. The ledger (gdpr_requests) is the source of truth
	// so the rate-limit survives process restart and is observable
	// by the same DPO query that reads the action history.
	//
	// First call inside the 24h window is allowed; the second call
	// gets 429 + Retry-After. Idempotent retries (PR-5.2) bypass
	// the rate-limit because the prior ledger row already paid for
	// the slot — the customer's second call is logically the same
	// action as the first, not a new action.
	//
	// We intentionally count the in-flight request *before*
	// gathering the bundle so a concurrent retry cannot both
	// succeed — the check is racy with a parallel caller only at
	// the millisecond boundary, which is fine because the cost of
	// a double-bundle (one customer's bundle) is bounded.
	rateSince := time.Now().UTC().Add(-api.ExportRateLimitWindow)
	if !isIdempotentRetry {
		n, err := s.store.CountGdprRequestsSince(r.Context(), acct.ID, string(state.GdprActionExport), rateSince)
		if err != nil {
			// A failing ledger query should not block an export — log
			// warn and proceed (mirrors the X-Audit-Logged best-effort
			// posture below). The customer is not worse off than before
			// PR-5.1; the rate-limit is a new affordance, not a
			// load-bearing security gate.
			s.log.Warn("apid: export rate-limit query failed",
				"account", acct.ID, "err", err)
		} else if n > 0 {
			// Compute seconds-until-reset so Retry-After is precise.
			// Walk the recent ledger for the most-recent export so the
			// hint matches reality (not just "24h from now").
			retryAfterS := api.ExportRateLimitWindowSeconds
			if recents, lerr := s.store.ListGdprRequestsForAccount(r.Context(), acct.ID, n); lerr == nil {
				for _, rec := range recents {
					if rec.Action == state.GdprActionExport && !rec.RequestedAt.IsZero() {
						resetAt := rec.RequestedAt.Add(api.ExportRateLimitWindow)
						secs := int(time.Until(resetAt).Seconds())
						if secs > retryAfterS {
							retryAfterS = secs
						}
						break
					}
				}
			}
			api.WriteProblem(w, api.ErrExportRateLimited(retryAfterS))
			return
		}
	}
	include := r.URL.Query().Get("include_secrets") != includeSecretsFalse
	spool, byteCount, err := prepareAccountExport(r.Context(), s, acct, include)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not assemble export"))
		return
	}
	defer cleanupAccountExportSpool(s, spool)
	// GDPR audit ledger — record that an export was served. Best-
	// effort; the ledger survives DeleteAccount so a future DPO can
	// see the request even after the account row is gone. PR #83
	// review #5: a ledger INSERT failure is now surfaced as
	// X-Audit-Logged: false so the customer can tell (in DevTools,
	// by mtime of the bundle, etc.) that their export landed in
	// their hands but did not make it into the audit trail.
	//
	// Idempotent retries (PR-5.2) skip the ledger insert: the prior
	// call already wrote the durable receipt. A retry that double-
	// inserted would inflate the rate-limit count for the customer's
	// own retries — a self-inflicted 429 after a flaky network is the
	// worst possible UX. The prior row's id is enough for the DPO to
	// re-derive the receipt.
	if isIdempotentRetry {
		w.Header().Set("X-Idempotent-Replay", "true")
	} else if !s.recordGdprRequest(r.Context(), acct, state.GdprActionExport, middleware.RequestIDFrom(r)) {
		w.Header().Set("X-Audit-Logged", "false")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		`attachment; filename="faas-account-`+acct.ID+`-`+
			time.Now().UTC().Format("20060102")+`.json"`)
	// Audit row for the customer-facing export action (issue #755
	// / PR-5.4). Distinct from the gdpr_requests ledger row above:
	// the audit row is the events-table side of the same action,
	// carrying structured data that downstream consumers (DPO
	// dashboards, regulator exports) can query. byte_count is the
	// pre-write size so a forensic auditor can detect a
	// tampering-in-flight scenario (the count in the audit row
	// vs the count the customer received). request_id is the
	// inbound id when the customer supplied one — same id that
	// the gdpr_requests ledger row carries, so a DPO can join
	// the two tables on it. Best-effort: the auditor is
	// non-blocking (spec §5.1) so a backlog cannot block exports.
	acctID := acct.ID
	s.audit.Emit(r.Context(), "account.export_requested", &acctID, map[string]any{
		"request_id": middleware.RequestIDFrom(r),
		"byte_count": byteCount,
		"replay":     isIdempotentRetry,
	})
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, spool); err != nil {
		s.log.Warn("apid: stream account export failed", "account", acct.ID, "err", err)
	}
}

// deleteAccount schedules the account for hard delete in 30 days.
// Idempotent: a second DELETE while already in deleted_pending
// returns the same envelope (status + scheduled_at + restore_until)
// without re-stamping the timestamp or re-sending the email.
func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	fresh, err := s.scheduleDeletion(r.Context(), acct, "rest")
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
//
// via is the surface that initiated the deletion ("rest" or
// "dashboard"); it's threaded through so the IAM-4 audit emit can
// attribute the action to the right caller (the dashboard form vs
// the REST DELETE).
func (s *server) scheduleDeletion(ctx context.Context, acct state.Account, via string) (state.Account, *api.Problem) {
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
			// Capture mailer errors explicitly (PR #83 review #4): a
			// silent email failure is the most expensive kind — the
			// customer gets a 200, the deletion clock is running, and
			// they have no record. Warn-log at minimum so a future
			// audit can correlate "missing email" with "delivery outage".
			if err := s.mailer.Send(ctx, Message{
				To: []string{fresh.Email}, Subject: subject, TextBody: body,
			}); err != nil {
				s.log.Warn("apid: pending deletion email send failed",
					"account", fresh.ID, "err", err)
			}
			// Emit pg_notify so any subscriber (audit, schedd's
			// live-instance evictor once it lands) can react without
			// polling. The payload matches the contract in
			// pkg/db/notify.go::NotifyAccountDeletionPending. Failure
			// here is a Warn, not a 5xx — the deletion row is the
			// source of truth and pkg/grace still reaps on schedule.
			if s.notif != nil {
				payload, _ := json.Marshal(deletionPendingPayload{
					AccountID:    fresh.ID,
					ScheduledAt:  fresh.DeletionRequestedAt.UTC().Format(time.RFC3339Nano),
					RestoreUntil: restoreUntil.UTC().Format(time.RFC3339Nano),
				})
				if err := s.notif.Notify(ctx, db.NotifyAccountDeletionPending, string(payload)); err != nil {
					s.log.Warn("apid: notify account_deletion_pending failed",
						"account", fresh.ID, "err", err)
				}
			}
			// GDPR self-serve audit ledger — captures the request at
			// the moment it landed so a customer (or a DPO) can be
			// shown proof of erasure against email + timestamp. The
			// row outlives DeleteAccount; pkg/grace stamps
			// completed_at after the hard-delete fires.
			s.recordGdprRequest(ctx, fresh, state.GdprActionDelete, "")
			// IAM-4 (ADR-035): record the deletion scheduling.
			// data.via lets the operator distinguish a dashboard
			// form submission from a CLI/API DELETE — useful when
			// a customer's session cookie was stolen and the
			// attacker prefers the dashboard surface.
			s.audit.Emit(ctx, "account.deletion_scheduled", &fresh.ID, map[string]any{
				"via": via,
			})
		}
		return fresh, nil
	}
	return acct, nil
}

// recordGdprRequest appends a single row to the gdpr_requests
// ledger and stamps completed_at if the action is "complete on
// insert" (export: the bundle is in hand, restore: the row is
// restored). The bool return tells the caller whether the row
// landed in the database so it can surface X-Audit-Logged: false
// when it didn't. Failures are logged Warn — never 5xx — so a
// flaky audit DB can never block a customer's GDPR action.
// Mirrors the pg_notify best-effort posture in scheduleDeletion
// above.
//
// requestID is the inbound X-Request-Id when the caller supplied
// one (issue #755 / PR-5.2) — empty when not. The id is recorded on
// the ledger row so the idempotency probe can find the prior call
// if a customer retries. Recorded verbatim (no sanitization) because
// the id is generated by the customer's stack (CLI, dashboard,
// retry library) and any sanitization would mask legitimate
// correlation ids.
func (s *server) recordGdprRequest(ctx context.Context, acct state.Account, action state.GdprAction, requestID string) bool {
	req := state.GdprRequest{
		ID:           uuid.NewString(),
		AccountID:    acct.ID,
		AccountEmail: acct.Email,
		Action:       action,
		RequestedAt:  time.Now().UTC(),
		RequestID:    requestID,
	}
	// Export + restore complete at insert time; delete completes
	// when pkg/grace fires DeleteAccount and calls
	// CompleteGdprRequest.
	switch action {
	case state.GdprActionExport, state.GdprActionRestore:
		req.CompletedAt = req.RequestedAt
	}
	if err := s.store.AppendGdprRequest(ctx, req); err != nil {
		s.log.Warn("apid: append gdpr_requests failed",
			"account", acct.ID, "action", string(action), "err", err)
		return false
	}
	return true
}

// restoreAccount flips the account back to active iff inside the
// 30-day grace window. Past grace → 409 account_not_restorable.
func (s *server) restoreAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	fresh, prob := s.cancelDeletion(r.Context(), acct, "rest")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	s.recordGdprRequest(r.Context(), fresh, state.GdprActionRestore, "")
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), fresh, r))
}

// cancelDeletion is the business-logic core reused by both the REST
// handler (restoreAccount) and the dashboard form handler. Returns
// (refreshed-account, problem). A nil problem means success.
//
// via is the surface that initiated the restore ("rest" or
// "dashboard"); threaded through so the IAM-4 audit emit can
// attribute the action to the right caller.
func (s *server) cancelDeletion(ctx context.Context, acct state.Account, via string) (state.Account, *api.Problem) {
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
	// IAM-4 (ADR-035): record the deletion restore. Pairs with the
	// account.deletion_scheduled row emitted inside
	// scheduleDeletion so a customer can trace the full lifecycle
	// of a near-deletion in their audit timeline.
	s.audit.Emit(ctx, "account.deletion_restored", &fresh.ID, map[string]any{
		"via": via,
	})
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
			"FAAS_DPA_PATH is unset; contact support@gregale.dev for the DPA"))
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

const accountExportPageSize = 250

// accountExportJSON writes one valid JSON object incrementally. Each resource
// section is released before the next one is loaded, and large history tables
// use keyset pages. The caller writes to an unlinked private spool first so a
// late store failure never exposes a partial bundle with a 200 response.
type accountExportJSON struct {
	w      io.Writer
	enc    *json.Encoder
	fields int
}

func newAccountExportJSON(w io.Writer) *accountExportJSON {
	return &accountExportJSON{w: w, enc: json.NewEncoder(w)}
}

func (j *accountExportJSON) beginField(name string) error {
	prefix := "{"
	if j.fields > 0 {
		prefix = ","
	}
	j.fields++
	_, err := io.WriteString(j.w, prefix+strconv.Quote(name)+":")
	return err
}

func (j *accountExportJSON) value(name string, value any) error {
	if err := j.beginField(name); err != nil {
		return err
	}
	return j.enc.Encode(value)
}

func (j *accountExportJSON) array(name string, produce func(emit func(any) error) error) error {
	if err := j.beginField(name); err != nil {
		return err
	}
	if _, err := io.WriteString(j.w, "["); err != nil {
		return err
	}
	first := true
	emit := func(value any) error {
		if !first {
			if _, err := io.WriteString(j.w, ","); err != nil {
				return err
			}
		}
		first = false
		return j.enc.Encode(value)
	}
	if err := produce(emit); err != nil {
		return err
	}
	_, err := io.WriteString(j.w, "]")
	return err
}

func (j *accountExportJSON) close() error {
	if j.fields == 0 {
		_, err := io.WriteString(j.w, "{}")
		return err
	}
	_, err := io.WriteString(j.w, "}\n")
	return err
}

func prepareAccountExport(ctx context.Context, s *server, acct state.Account, includeSecrets bool) (*os.File, int64, error) {
	spool, err := os.CreateTemp("", "gregale-account-export-*.json")
	if err != nil {
		return nil, 0, err
	}
	// Linux and macOS keep the descriptor readable after unlink. Removing the
	// directory entry immediately means an apid crash cannot leave a customer
	// export behind in the host temporary directory.
	if err := os.Remove(spool.Name()); err != nil {
		_ = spool.Close()
		return nil, 0, err
	}
	fail := func(cause error) (*os.File, int64, error) {
		_ = spool.Close()
		_ = os.Remove(spool.Name())
		return nil, 0, cause
	}
	if err := writeAccountExportJSON(ctx, s, acct, includeSecrets, spool); err != nil {
		return fail(err)
	}
	info, err := spool.Stat()
	if err != nil {
		return fail(err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return spool, info.Size(), nil
}

func cleanupAccountExportSpool(s *server, spool *os.File) {
	name := spool.Name()
	if err := spool.Close(); err != nil {
		s.log.Warn("apid: close account export spool failed", "err", err)
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("apid: remove account export spool failed", "err", err)
	}
}

func writeAccountExportJSON(ctx context.Context, s *server, acct state.Account, includeSecrets bool, dst io.Writer) error {
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		return fmt.Errorf("list apps: %w", err)
	}
	out := newAccountExportJSON(dst)
	if err := out.value("exported_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	//nolint:contextcheck // nil store skips the response's derived lookups.
	if err := out.value("account", s.accountResponse(context.Background(), acct, nil)); err != nil {
		return err
	}
	if err := out.array("apps", func(emit func(any) error) error {
		for _, app := range apps {
			if err := emit(s.appResponse(app, acct.Plan)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	depByID := make(map[string]string)
	if err := out.array("deployments", func(emit func(any) error) error {
		var beforeAt time.Time
		var beforeID string
		for {
			rows, err := s.store.ListDeploymentsForAccountPage(ctx, acct.ID, beforeAt, beforeID, accountExportPageSize)
			if err != nil {
				return fmt.Errorf("list deployments: %w", err)
			}
			shaped, err := buildDeploymentsForExport(rows)
			if err != nil {
				return err
			}
			for i, deployment := range shaped {
				depByID[rows[i].ID] = rows[i].AppID
				if err := emit(deployment); err != nil {
					return err
				}
			}
			if len(rows) < accountExportPageSize {
				return nil
			}
			last := rows[len(rows)-1]
			if last.CreatedAt.Equal(beforeAt) && last.ID == beforeID {
				return errors.New("deployment export cursor did not advance")
			}
			beforeAt, beforeID = last.CreatedAt, last.ID
		}
	}); err != nil {
		return err
	}

	sections := []struct {
		name string
		load func() (any, error)
	}{
		{"builds", func() (any, error) { return listBuildsForAccountExport(ctx, s.store, acct.ID, depByID) }},
		{"instances", func() (any, error) { return listInstancesForAccountExport(ctx, s.store, acct.ID) }},
		{"usage", func() (any, error) { return listUsageForAccountExport(ctx, s.store, acct.ID) }},
		{"domains", func() (any, error) { return listDomainsForAccountExport(ctx, s.store, acct.ID) }},
		{"crons", func() (any, error) { return listCronsForAccountExport(ctx, s.store, acct.ID) }},
		{"api_keys", func() (any, error) { return listKeysForAccountExport(ctx, s.store, acct.ID) }},
	}
	for _, section := range sections {
		value, err := section.load()
		if err != nil {
			return fmt.Errorf("load %s: %w", section.name, err)
		}
		if err := out.value(section.name, value); err != nil {
			return err
		}
	}

	if err := out.array("app_secrets", func(emit func(any) error) error {
		if !includeSecrets {
			return nil
		}
		for _, app := range apps {
			rows, err := s.store.ListAllAppSecrets(ctx, acct.ID, app.ID)
			if err != nil {
				return fmt.Errorf("list secrets app=%s: %w", app.ID, err)
			}
			for _, sec := range rows {
				if err := emit(api.AppSecretExportResponse{
					AppID: sec.AppID, Scope: sec.Scope, Key: sec.Key,
					Ciphertext: base64.RawURLEncoding.EncodeToString(sec.Ciphertext),
					CreatedAt:  sec.CreatedAt.UTC().Format(time.RFC3339),
					UpdatedAt:  sec.UpdatedAt.UTC().Format(time.RFC3339),
				}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := writeAccountAuditTrail(ctx, s.store, acct.ID, out); err != nil {
		return err
	}
	return out.close()
}

type auditExportItem struct {
	at  time.Time
	dto api.GdprAuditExportResponse
}

type gdprExportPager struct {
	store     state.Store
	accountID string
	beforeAt  time.Time
	beforeID  string
	rows      []state.GdprRequest
	pos       int
	exhausted bool
}

func (p *gdprExportPager) next(ctx context.Context) (auditExportItem, bool, error) {
	if p.pos >= len(p.rows) {
		if p.exhausted {
			return auditExportItem{}, false, nil
		}
		rows, err := p.store.ListGdprRequestsForAccountPage(ctx, p.accountID, p.beforeAt, p.beforeID, accountExportPageSize)
		if err != nil {
			return auditExportItem{}, false, err
		}
		p.rows, p.pos = rows, 0
		p.exhausted = len(rows) < accountExportPageSize
		if len(rows) == 0 {
			return auditExportItem{}, false, nil
		}
		last := rows[len(rows)-1]
		p.beforeAt, p.beforeID = last.RequestedAt, last.ID
	}
	row := p.rows[p.pos]
	p.pos++
	return auditExportItem{at: row.RequestedAt, dto: api.GdprAuditExportResponse{
		Source: "gdpr", Action: string(row.Action),
		RequestedAt: row.RequestedAt.UTC().Format(time.RFC3339),
		CompletedAt: formatTimeOrEmpty(row.CompletedAt),
	}}, true, nil
}

type eventExportPager struct {
	store     state.Store
	accountID string
	beforeAt  time.Time
	beforeID  int64
	rows      []state.Event
	pos       int
	exhausted bool
}

func (p *eventExportPager) next(ctx context.Context) (auditExportItem, bool, error) {
	if p.pos >= len(p.rows) {
		if p.exhausted {
			return auditExportItem{}, false, nil
		}
		rows, err := p.store.ListEventsPage(ctx, p.accountID, p.beforeAt, p.beforeID, accountExportPageSize)
		if err != nil {
			return auditExportItem{}, false, err
		}
		p.rows, p.pos = rows, 0
		p.exhausted = len(rows) < accountExportPageSize
		if len(rows) == 0 {
			return auditExportItem{}, false, nil
		}
		last := rows[len(rows)-1]
		p.beforeAt, p.beforeID = last.At, last.ID
	}
	row := p.rows[p.pos]
	p.pos++
	return auditExportItem{at: row.At, dto: api.GdprAuditExportResponse{
		Source: "event", RequestedAt: row.At.UTC().Format(time.RFC3339),
		Kind: row.Kind, Data: row.Data,
	}}, true, nil
}

func writeAccountAuditTrail(ctx context.Context, store state.Store, accountID string, out *accountExportJSON) error {
	gdpr := &gdprExportPager{store: store, accountID: accountID}
	events := &eventExportPager{store: store, accountID: accountID}
	g, hasG, err := gdpr.next(ctx)
	if err != nil {
		return fmt.Errorf("list GDPR audit: %w", err)
	}
	e, hasE, err := events.next(ctx)
	if err != nil {
		return fmt.Errorf("list account events: %w", err)
	}
	if !hasG && !hasE {
		return nil
	}
	return out.array("audit_trail", func(emit func(any) error) error {
		for hasG || hasE {
			if hasG && (!hasE || !g.at.Before(e.at)) {
				if err := emit(g.dto); err != nil {
					return err
				}
				g, hasG, err = gdpr.next(ctx)
			} else {
				if err := emit(e.dto); err != nil {
					return err
				}
				e, hasE, err = events.next(ctx)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// getGraceWindow returns the current per-account grace override
// (issue #189 / IAM-5). The endpoint is admin-only — the rotation
// primitive is admin-only, and the override is meaningless for a
// non-admin caller. The PlanDefault field is unconditionally
// populated so the dashboard can render the "Override: N / Plan
// default: 7" pair without a second round-trip.
//
// Auth: admin scope (ScopesAdminOnly); middleware already
// short-circuits non-admin callers.
func (s *server) getGraceWindow(w http.ResponseWriter, r *http.Request, acct state.Account) {
	days, err := s.store.GetAccountKeyGraceWindow(r.Context(), acct.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read grace window"))
		return
	}
	writeJSON(w, http.StatusOK, api.GraceWindowResponse{
		Days:        days,
		PlanDefault: api.DefaultAPIKeyGraceWindowDays,
	})
}

// setGraceWindow writes the per-account grace override and
// invalidates the in-process cache so the next rotation sees the
// new value (issue #189 / IAM-5). The body is {days: 0|7|null};
// nil clears the override and falls back to the plan default (7).
// Days < 0 is rejected with 400 (negative days is meaningless) and
// emits no audit row.
//
// Auth: admin scope (ScopesAdminOnly).
//
// Audit: key.grace_window_set carries both old and new values so
// the dashboard can render the toggle history. The audit row is
// the only place the OLD value is observable — once the PATCH
// lands, the cache is empty and the next read is the new value.
func (s *server) setGraceWindow(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.SetGraceWindowRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid body", err.Error()))
		return
	}
	if req.Days != nil && *req.Days < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid grace window",
			"days must be >= 0 (0 = atomic rotation, null = use plan default)"))
		return
	}

	// Read the prior value for the audit row. The cache may
	// short-circuit this — invalidated-or-stale cache is fine.
	oldDays, _ := s.resolveGraceWindow(r.Context(), acct.ID) // best-effort
	_ = s.store.SetAccountKeyGraceWindow(r.Context(), acct.ID, req.Days)
	if s.graceWindowCache != nil {
		s.graceWindowCache.Invalidate(acct.ID)
	}
	s.audit.Emit(r.Context(), "key.grace_window_set", &acct.ID, map[string]any{
		"old_days": oldDays,
		"new_days": req.Days, // *int — JSON null for the cleared case
	})
	writeJSON(w, http.StatusOK, api.GraceWindowResponse{
		Days:        req.Days,
		PlanDefault: api.DefaultAPIKeyGraceWindowDays,
	})
}

// getAccountEgressAllowlistExtra returns the per-account additive
// budget on top of the plan's apps.egress_allowlist cap (issue #679
// / PR-B / ADR-082). The endpoint is admin-only — the override is
// meaningless for a non-admin caller (the customer-facing validator
// reads the field, the admin endpoint is the only place it's
// written). The PlanCap field is unconditionally populated so the
// dashboard can render the "Override: N / Plan cap: 16" pair
// without a second round-trip.
//
// Auth: admin scope (ScopesAdminOnly); middleware already
// short-circuits non-admin callers.
func (s *server) getAccountEgressAllowlistExtra(w http.ResponseWriter, r *http.Request, acct state.Account) {
	extra, err := s.store.GetAccountEgressAllowlistExtra(r.Context(), acct.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read egress allowlist extra"))
		return
	}
	writeJSON(w, http.StatusOK, api.AccountEgressAllowlistExtraResponse{
		Extra:    extra,
		PlanCap:  acct.Plan.EgressAllowlistMaxSize(),
		MaxExtra: api.MaxAccountEgressAllowlistExtra,
	})
}

// setAccountEgressAllowlistExtra writes the per-account additive
// budget (issue #679 / PR-B / ADR-082). The body is
// {extra: 0|positive|null-bound}; negative values are rejected with
// 400 (api.ErrAccountEgressAllowlistExtraOutOfRange) and emit no
// audit row. Values > api.MaxAccountEgressAllowlistExtra (1024) are
// rejected with the same 400 — the cap is intentional, see the
// comment on api.MaxAccountEgressAllowlistExtra. Extra == 0 clears
// the override (the plan cap is authoritative again).
//
// Auth: admin scope (ScopesAdminOnly).
//
// Audit: account.egress_allowlist_extra_set carries both old and
// new values so the dashboard can render the toggle history. The
// audit row is the only place the OLD value is observable — once
// the PATCH lands, the next read is the new value.
func (s *server) setAccountEgressAllowlistExtra(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.SetAccountEgressAllowlistExtraRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid body", err.Error()))
		return
	}
	if req.Extra < 0 || req.Extra > api.MaxAccountEgressAllowlistExtra {
		api.WriteProblem(w, api.ErrAccountEgressAllowlistExtraOutOfRange(req.Extra, api.MaxAccountEgressAllowlistExtra))
		return
	}

	// Read the prior value for the audit row. Best-effort —
	// ErrNotFound is acceptable (a fresh account has no override
	// yet, the field defaults to 0).
	oldExtra, _ := s.store.GetAccountEgressAllowlistExtra(r.Context(), acct.ID) // best-effort
	if err := s.store.SetAccountEgressAllowlistExtra(r.Context(), acct.ID, req.Extra); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not write egress allowlist extra"))
		return
	}
	s.audit.Emit(r.Context(), "account.egress_allowlist_extra_set", &acct.ID, map[string]any{
		"old_extra": oldExtra,
		"new_extra": req.Extra,
		"plan_cap":  acct.Plan.EgressAllowlistMaxSize(),
		"max_extra": api.MaxAccountEgressAllowlistExtra,
	})
	writeJSON(w, http.StatusOK, api.AccountEgressAllowlistExtraResponse{
		Extra:    req.Extra,
		PlanCap:  acct.Plan.EgressAllowlistMaxSize(),
		MaxExtra: api.MaxAccountEgressAllowlistExtra,
	})
}

// --- per-resource list helpers (each ≤50 LoC, exported for tests) -------

// buildDeploymentsForExport shapes pre-fetched deployment rows into
// the DTO list. Reads no store — the gatherExport caller already
// fetched them once for the deploymentID→appID map (see Bug 4 fix).
func buildDeploymentsForExport(rows []state.Deployment) ([]api.DeploymentResponse, error) {
	out := make([]api.DeploymentResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, api.DeploymentResponse{
			ID: d.ID, AppID: d.AppID, BuildID: d.BuildID,
			ImageDigest: d.ImageDigest, Kind: string(d.Kind),
			Status: string(d.Status), Error: sanitizeExportString(d.Error),
			ErrorCode: d.ErrorCode,
			ErrorHint: d.ErrorHint, ErrorWhy: d.ErrorWhy,
			ErrorFix: d.ErrorFix, ErrorRelevantLogs: d.ErrorRelevantLogs,
			CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
			// ADR-091 / PR-D: scope echo on export fixtures so the
			// GDPR export carries the per-deployment env-targeting
			// context. Stamped from dep.Scope (already populated by
			// the SELECT projection in pgstore.DeploymentByID /
			// ListDeployments*).
			Scope: d.Scope,
			// Issue #977 / ADR-116: annotation echo on export
			// fixtures. The four columns are operator-supplied
			// metadata (free-text reason, closed-set tag, actor
			// label, PR number). sanitizeExportString is applied
			// to reason / tag / deployed_by so a customer who
			// pasted PII into reason is still covered by the
			// existing export scrubber.
			Reason:     sanitizeExportString(d.Reason),
			Tag:        sanitizeExportString(d.Tag),
			DeployedBy: sanitizeExportString(d.DeployedBy),
			PRNumber:   d.PRNumber,
		})
	}
	return out, nil
}

// listBuildsForAccountExport now accepts the deployment→app map so
// it can populate BuildExportResponse.AppID. Falls back to "" when
// a build's DeploymentID is not in the map (shouldn't happen, but
// keeps the export deterministic if it ever does).
func listBuildsForAccountExport(ctx context.Context, st state.Store, accountID string, depByID map[string]string) ([]api.BuildExportResponse, error) {
	rows, err := st.ListBuildsForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]api.BuildExportResponse, 0, len(rows))
	for _, b := range rows {
		out = append(out, api.BuildExportResponse{
			ID: b.ID, DeploymentID: b.DeploymentID,
			AppID:  depByID[b.DeploymentID],
			Kind:   string(b.Kind),
			Status: string(b.Status), SourceBytes: b.SourceBytes,
			StartedAt:  b.StartedAt.UTC().Format(time.RFC3339),
			FinishedAt: b.FinishedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func listInstancesForAccountExport(ctx context.Context, st state.Store, accountID string) ([]api.InstanceResponse, error) {
	rows, err := st.ListInstancesForAccount(ctx, accountID)
	if err != nil {
		return nil, err
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
	return out, nil
}

func listUsageForAccountExport(ctx context.Context, st state.Store, accountID string) ([]api.UsageExportResponse, error) {
	rows, err := st.UsageByAccount(ctx, accountID, time.Time{})
	if err != nil {
		return nil, err
	}
	out := make([]api.UsageExportResponse, 0, len(rows))
	for _, u := range rows {
		out = append(out, api.UsageExportResponse{
			AppID: u.AppID, Month: u.Month.UTC().Format("2006-01"),
			MBSeconds: u.MBSeconds, Requests: u.Requests,
			// CPUUsageUsec is the per-(app, month) CPU-µs
			// informational field (issue #279 / PR-B). 0
			// when the meterd sampler has not accumulated
			// any CPU for this row yet.
			CPUUsageUsec: u.CPUUsec,
			// ADR-046 (step 10): per-(app, month) egress
			// bytes — informational only, not billed.
			// Mirrors UsageResponse.TXBytes / NetTxBytes.
			// Gateway-side tx_bytes producer lands in
			// PR-2.
			TXBytes:    u.TXBytes,
			NetTxBytes: u.NetTxBytes,
			// ADR-048: ingress + cold-boot transitions.
			// Informational only — not billed.
			NetRxBytes:    u.NetRxBytes,
			ColdBootCount: u.ColdBootCount,
		})
	}
	return out, nil
}

func listDomainsForAccountExport(ctx context.Context, st state.Store, accountID string) ([]api.CustomDomainResponse, error) {
	rows, err := st.ListDomainsForAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]api.CustomDomainResponse, 0, len(rows))
	for _, d := range rows {
		status := d.CertStatus
		if status == "" {
			status = state.CustomDomainCertPending
		}
		resp := api.CustomDomainResponse{
			Domain:           d.Domain,
			AppID:            d.AppID,
			Verified:         d.Verified(),
			VerifiedAt:       formatTimeOrEmpty(d.VerifiedAt),
			CertStatus:       string(status),
			CertLastError:    d.CertLastError,
			CertExpiresAt:    formatTimeOrEmpty(d.CertExpiresAt),
			DNSLastCheckedAt: formatTimeOrEmpty(d.DNSLastCheckedAt),
		}
		resp.CertNotAfter = resp.CertExpiresAt
		out = append(out, resp)
	}
	return out, nil
}

func listCronsForAccountExport(ctx context.Context, st state.Store, accountID string) ([]api.CronResponse, error) {
	rows, err := st.ListCronsForAccount(ctx, accountID)
	if err != nil {
		return nil, err
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
	return out, nil
}

func listKeysForAccountExport(ctx context.Context, st state.Store, accountID string) ([]api.APIKeyExportResponse, error) {
	rows, err := st.ListAPIKeys(ctx, accountID)
	if err != nil {
		return nil, err
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
	return out, nil
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
