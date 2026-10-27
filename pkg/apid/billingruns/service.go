// Package billingruns bills one ended UTC month for every customer in a
// single request or on a monthly schedule (ADR-1503). A background worker
// drafts, completeness-checks, finalizes and optionally exports each
// customer's statement. Like the other apid domains (ADR-982), it reaches
// apid only through Host; apidserver keeps the route table and wiring.
package billingruns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what billing runs need from apid (ADR-982). Statement methods
// apply the same rules, audit events and webhooks as the statement
// endpoints; a problem with status 500 or above is transient.
type Host interface {
	Store() state.Store
	// ConsumerFeatureAllowed reports whether the plan includes API
	// consumers; on false it has already written the problem.
	ConsumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool
	Audit(ctx context.Context, event, accountID string, fields map[string]any)

	DraftConsumerStatement(ctx context.Context, acct state.Account, appID, consumerID string, start, end time.Time) (state.APIConsumerUsageStatement, *api.Problem)
	FinalizeConsumerStatement(ctx context.Context, acct state.Account, appID, consumerID, statementID string) (state.APIConsumerUsageStatement, *api.Problem)
	// ConsumerCompleteness compares the consumer's ledger with request
	// telemetry over the period (ADR-954).
	ConsumerCompleteness(ctx context.Context, acct state.Account, appID, consumerID string, start, end time.Time) (status string, missing int64, err error)
	ExportConsumerStatement(ctx context.Context, acct state.Account, consumer state.APIConsumer, statement state.APIConsumerUsageStatement, provider string) (state.StatementInvoiceExport, *api.Problem)

	DraftTenantStatement(ctx context.Context, acct state.Account, tenantID string, start, end time.Time) (state.PlatformTenantStatement, *api.Problem)
	FinalizeTenantStatement(ctx context.Context, acct state.Account, tenantID, statementID string) (state.PlatformTenantStatement, *api.Problem)
	ExportTenantStatement(ctx context.Context, acct state.Account, tenant state.PlatformTenant, statement state.PlatformTenantStatement, provider string) (state.StatementInvoiceExport, *api.Problem)

	// ProductControlEnabled reports FAAS_PRODUCT_CONTROL_ENABLED; runs bill
	// Product Control customers only while it is on (ADR-20261018212918).
	ProductControlEnabled() bool
	FinalizeProductStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope, reviewHash string) (api.ProductBillingDocument, *api.Problem)
	ExportProductStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope, documentID, provider string) (api.StatementInvoiceExportResponse, *api.Problem)
	// SendEmail sends a plain-text notification (ADR-1504); messageID is the
	// provider idempotency key.
	SendEmail(ctx context.Context, to, subject, body, messageID string) error
}

// Service serves billing run endpoints and runs the worker. Now is the
// clock; nil uses time.Now.
type Service struct {
	Host Host
	Now  func() time.Time
	// Enabled pauses the background worker while it reports false: API
	// monetization is released by a runtime setting (ADR-20261018090210).
	// Nil runs it.
	Enabled func() bool
	// Released reports whether API monetization is released to an account
	// (ADR-20261020170649); the worker skips accounts it is not. Nil
	// releases every account.
	Released func(accountID string) bool
}

// released reports whether the worker serves an account's monetization.
func (s *Service) released(accountID string) bool { return s.Released == nil || s.Released(accountID) }

// tick runs one background tick unless Enabled pauses the worker; paused,
// nothing starts, advances or notifies.
func (s *Service) tick(ctx context.Context) error {
	if s.Enabled != nil && !s.Enabled() {
		return nil
	}
	return s.RunOnce(ctx)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) runStore(w http.ResponseWriter) (state.BillingRunStore, bool) {
	store, ok := s.Host.Store().(state.BillingRunStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("billing runs are unavailable"))
	}
	return store, ok
}

func validationProblem(title, detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, title, detail)
}

// runMonth parses YYYY-MM and requires the month to have ended at least
// BillingRunSettleDelay ago.
func runMonth(month string, now time.Time) (time.Time, *api.Problem) {
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}, validationProblem("Invalid month", "month must be YYYY-MM")
	}
	if ready := start.AddDate(0, 1, 0).Add(api.BillingRunSettleDelay); now.Before(ready) {
		return time.Time{}, validationProblem("Month not ended",
			fmt.Sprintf("a billing run for %s can start at %s, %s after the month ends", month, ready.Format(time.RFC3339), api.BillingRunSettleDelay))
	}
	return start.UTC(), nil
}

// runOptions applies defaults: both sources, finalize, no export, hold on
// completeness gaps.
func runOptions(sources []string, finalize *bool, export string, holdOnGaps *bool) (state.BillingRunOptions, *api.Problem) {
	opts := state.BillingRunOptions{Sources: slices.Clone(sources), Finalize: true, Export: export, HoldOnGaps: true}
	if len(opts.Sources) == 0 {
		opts.Sources = []string{state.BillingRunSourcePlatformTenants, state.BillingRunSourceAppConsumers}
	}
	if finalize != nil {
		opts.Finalize = *finalize
	}
	if holdOnGaps != nil {
		opts.HoldOnGaps = *holdOnGaps
	}
	if opts.Export == "" {
		opts.Export = state.BillingRunExportNone
	}
	if err := state.ValidateBillingRunOptions(opts); err != nil {
		return opts, validationProblem("Invalid billing run options", strings.TrimPrefix(err.Error(), state.ErrInvalidArgument.Error()+": "))
	}
	return opts, nil
}

// requireProductControl rejects the product_customers source while Product
// Control is disabled (ADR-20261018212918).
func (s *Service) requireProductControl(opts state.BillingRunOptions) *api.Problem {
	if opts.Includes(state.BillingRunSourceProductCustomers) && !s.Host.ProductControlEnabled() {
		return validationProblem("Product Control is disabled", "the product_customers source needs Product Control to be enabled")
	}
	return nil
}

// requireExportTarget rejects an export to a provider with no connected
// key (ADR-1507).
func (s *Service) requireExportTarget(ctx context.Context, acct state.Account, opts state.BillingRunOptions) *api.Problem {
	if opts.Export == state.BillingRunExportNone {
		return nil
	}
	exports, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return api.ErrInternal("statement invoice exports are unavailable")
	}
	if _, err := exports.GetBillingConnection(ctx, acct.ID, opts.Export); errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Billing provider not connected",
			fmt.Sprintf("connect %s first: PUT /v1/account/billing-connections/%s", opts.Export, opts.Export))
	} else if err != nil {
		return api.ErrInternal("could not load the " + opts.Export + " connection")
	}
	return nil
}

// CreateRun starts a billing run for an ended month (ADR-1503).
func (s *Service) CreateRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.Host.ConsumerFeatureAllowed(w, acct) {
		return
	}
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	var req api.CreateBillingRunRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	run, problem := s.requestedRun(r.Context(), acct, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.createRun(w, r, acct, store, run)
}

func (s *Service) requestedRun(ctx context.Context, acct state.Account, req api.CreateBillingRunRequest) (state.BillingRun, *api.Problem) {
	start, problem := runMonth(req.Month, s.now())
	if problem != nil {
		return state.BillingRun{}, problem
	}
	opts, problem := runOptions(req.Sources, req.Finalize, req.Export, req.HoldOnGaps)
	if problem != nil {
		return state.BillingRun{}, problem
	}
	if problem := s.requireProductControl(opts); problem != nil {
		return state.BillingRun{}, problem
	}
	if problem := s.requireExportTarget(ctx, acct, opts); problem != nil {
		return state.BillingRun{}, problem
	}
	return state.BillingRun{AccountID: acct.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0),
		BillingRunOptions: opts, Trigger: state.BillingRunTriggerAPI}, nil
}

func (s *Service) createRun(w http.ResponseWriter, r *http.Request, acct state.Account, store state.BillingRunStore, run state.BillingRun) {
	created, err := store.CreateBillingRun(r.Context(), run)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Billing run in progress",
			fmt.Sprintf("billing run %s for %s is %s; wait for it to complete", created.ID, created.PeriodStart.Format("2006-01"), created.Status)))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create the billing run"))
		return
	}
	s.Host.Audit(r.Context(), "billing_run.created", acct.ID, map[string]any{"run_id": created.ID,
		"month": created.PeriodStart.Format("2006-01"), "trigger": created.Trigger, "export": created.Export})
	httpkit.WriteJSON(w, http.StatusAccepted, runResponse(created))
}

// ListRuns lists the account's billing runs, newest first.
func (s *Service) ListRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	problem, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.BillingRunListDefault, api.BillingRunListMax, "billing runs")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	runs, err := store.ListBillingRuns(r.Context(), acct.ID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list billing runs"))
		return
	}
	out := api.BillingRunListResponse{Runs: make([]api.BillingRunResponse, 0, len(runs))}
	for _, run := range runs {
		out.Runs = append(out.Runs, runResponse(run))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// pathRunID reads the {id} path value; anything but a UUID is not found.
func pathRunID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpkit.NotFound(w, "no such billing run")
		return "", false
	}
	return id, true
}

// GetRun returns one billing run with its item counts.
func (s *Service) GetRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	runID, ok := pathRunID(w, r)
	if !ok {
		return
	}
	run, err := store.GetBillingRun(r.Context(), acct.ID, runID)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such billing run")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the billing run"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, runResponse(run))
}

// ListRunItems pages what a run did for each customer.
func (s *Service) ListRunItems(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runStore(w)
	if !ok {
		return
	}
	runID, ok := pathRunID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	outcome := q.Get("outcome")
	if outcome != "" && !slices.Contains(state.BillingRunItemOutcomes, outcome) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad outcome", "unknown item outcome"))
		return
	}
	problem, limit := api.ParseLimit(q.Get("limit"), api.BillingRunItemListDefault, api.BillingRunItemListMax, "billing run items")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	items, err := store.ListBillingRunItems(r.Context(), acct.ID, runID, outcome, q.Get("cursor"), limit)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such billing run")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list billing run items"))
		return
	}
	out := api.BillingRunItemListResponse{Items: make([]api.BillingRunItemResponse, 0, len(items))}
	for _, item := range items {
		out.Items = append(out.Items, itemResponse(item))
	}
	if len(items) == limit {
		out.NextCursor = items[len(items)-1].Key()
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func runResponse(run state.BillingRun) api.BillingRunResponse {
	c := run.Counts
	return api.BillingRunResponse{ID: run.ID, Month: run.PeriodStart.UTC().Format("2006-01"), PeriodStart: run.PeriodStart.UTC(),
		PeriodEnd: run.PeriodEnd.UTC(), Sources: slices.Clone(run.Sources), Finalize: run.Finalize, Export: run.Export,
		HoldOnGaps: run.HoldOnGaps, Trigger: run.Trigger, Status: run.Status, CreatedAt: run.CreatedAt.UTC(),
		StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
		Counts: api.BillingRunCounts{Pending: c[state.BillingRunItemPending], Drafted: c[state.BillingRunItemDrafted],
			Finalized: c[state.BillingRunItemFinalized], Exported: c[state.BillingRunItemExported],
			UpToDate: c[state.BillingRunItemUpToDate], Held: c[state.BillingRunItemHeld],
			Skipped: c[state.BillingRunItemSkipped], Failed: c[state.BillingRunItemFailed]}}
}

func itemResponse(item state.BillingRunItem) api.BillingRunItemResponse {
	out := api.BillingRunItemResponse{SourceKind: item.SourceKind, ExternalRef: item.ExternalRef, Outcome: item.Outcome,
		StatementID: item.StatementID, Revision: item.Revision, Currency: item.Currency, Completeness: item.Completeness,
		InvoiceID: item.InvoiceID, ExportedInvoices: item.ExportedCount, Reason: item.Reason, Attempts: item.Attempts,
		UpdatedAt: item.UpdatedAt.UTC(), PaymentStatus: item.PaymentStatus}
	switch item.SourceKind {
	case state.StatementSourceAppConsumer:
		out.AppID, out.ConsumerID = item.AppID, item.SourceID
	case state.StatementSourceProductControl:
		out.AppID, out.PlatformTenantID = item.AppID, item.SourceID
	default:
		out.PlatformTenantID = item.SourceID
	}
	if item.StatementID != "" {
		amount := item.AmountMillicents
		out.AmountMillicents = &amount
	}
	return out
}
