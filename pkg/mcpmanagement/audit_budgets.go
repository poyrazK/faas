package mcpmanagement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// Audit trail, spend forecast and notify-only budgets (ADR-20261118161500).
// The account audit log needs a connection consented to every app; a
// connection limited to some apps reads only those apps' audit events.
// Spend reads and budgets need usage:read. Agents create and edit only
// budgets that notify: a budget that rejects traffic or suspends work stays
// with people, and apid asks a person before every budget write.

const (
	maxBudgetPlans = 20
	budgetPlanTTL  = 15 * time.Minute
)

type auditArgs struct {
	App        string `json:"app,omitempty" jsonschema:"Only this app's events; required on a connection limited to some apps"`
	KindPrefix string `json:"kind_prefix,omitempty" jsonschema:"Only kinds starting with this, such as deploy. or env."`
	Since      string `json:"since,omitempty" jsonschema:"Only events at or after this RFC 3339 time"`
	Limit      int    `json:"limit,omitempty" jsonschema:"1 to 100; default 20"`
	Before     string `json:"before,omitempty" jsonschema:"Account log only: next_before from the previous page"`
}

type forecastArgs struct {
	Month string `json:"month,omitempty" jsonschema:"UTC month as YYYY-MM; default the current month"`
}

type planBudgetArgs struct {
	Op               string   `json:"op" jsonschema:"create or update"`
	BudgetID         string   `json:"budget_id,omitempty" jsonschema:"update: the budget"`
	Scope            string   `json:"scope,omitempty" jsonschema:"create: account or app; default app"`
	App              string   `json:"app,omitempty" jsonschema:"create with scope app: the app"`
	Name             string   `json:"name,omitempty" jsonschema:"Budget name; required to create, omit to keep on update"`
	Currency         string   `json:"currency,omitempty" jsonschema:"ISO currency such as usd; required to create, omit to keep on update"`
	Meters           []string `json:"meters,omitempty" jsonschema:"Meters to count; create: omit for all; update: omit to keep"`
	Basis            string   `json:"basis,omitempty" jsonschema:"net_usage or gross_usage; create default net_usage; update: omit to keep"`
	LimitMillicents  int64    `json:"limit_millicents,omitempty" jsonschema:"Monthly limit in millicents; required to create, omit to keep on update"`
	NotifyMillicents []int64  `json:"notify_millicents,omitempty" jsonschema:"Spend levels that notify, in millicents; update: omit to keep"`
	Enabled          *bool    `json:"enabled,omitempty" jsonschema:"create default true; update: omit to keep"`
	Reason           string   `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type budgetPlan struct {
	ID               string                `json:"plan_id"`
	Op               string                `json:"op"`
	AccountID        string                `json:"account_id"`
	BudgetID         string                `json:"budget_id,omitempty"`
	ExpectedRevision int64                 `json:"expected_revision,omitempty"`
	Spec             financial.BudgetSpec  `json:"spec"`
	Before           *financial.BudgetSpec `json:"before,omitempty"`
	Preview          any                   `json:"preview"`
	Reason           string                `json:"reason"`
	Effect           string                `json:"effect"`
	ExpiresAt        time.Time             `json:"expires_at"`
	receipt          any
}

func registerAuditBudgets(s *mcp.Server, m *management) {
	register(s, m, "gregale_list_audit_events", "Read the audit trail newest first: who changed what and when (deploys, variables, keys, domains, sign-ins). With app, only that app's events. Read-only.", m.listAuditEvents)
	if !m.hasCapability(api.ScopeUsageRead) {
		return
	}
	register(s, m, "gregale_get_spend_forecast", "Read the account's spend so far this month and the forecast for the month, by meter. Read-only.", m.getSpendForecast)
	register(s, m, "gregale_list_budgets", "List the account's spending budgets: scope, limit, notify levels, action and current spend. Read-only.", m.listBudgets)
	register(s, m, "gregale_plan_budget_change", "Review creating or updating a budget that notifies at spend levels; it never blocks traffic or stops work. Changes nothing; apply with gregale_apply_budget_change.", m.planBudgetChange)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_apply_budget_change", "Apply a reviewed budget plan. A person approves the write first.", false, m.applyBudgetChange)
	}
}

func (m *management) listAuditEvents(ctx context.Context, a auditArgs) (any, error) {
	limit, err := pageSize(a.Limit)
	if err != nil {
		return nil, err
	}
	if a.Since != "" {
		if _, err := time.Parse(time.RFC3339, a.Since); err != nil {
			return nil, invalid("invalid_argument", "since must be an RFC 3339 time")
		}
	}
	if a.KindPrefix != "" {
		if err := validateKindPrefix(a.KindPrefix); err != nil {
			return nil, err
		}
	}
	if a.App == "" {
		if m.opts.AllowedApps != nil {
			return nil, invalid("app_required", "this connection is limited to some apps; name one")
		}
		if len(a.Before) > 256 || strings.ContainsFunc(a.Before, unicode.IsControl) {
			return nil, invalid("invalid_argument", "before must be next_before from the previous page")
		}
		return m.client.ListAuditLogPage(ctx, a.Before, a.Since, a.KindPrefix, limit)
	}
	if a.Before != "" {
		return nil, invalid("invalid_argument", "before pages the account log; narrow an app's events with since")
	}
	app, err := m.consentedApp(ctx, a.App, false)
	if err != nil {
		return nil, err
	}
	return m.client.ListAuditEvents(ctx, a.Since, a.KindPrefix, app.ID, limit, false)
}

func validateKindPrefix(v string) error {
	if len(v) > 64 {
		return invalid("invalid_argument", "kind_prefix is at most 64 characters")
	}
	for _, r := range v {
		if !strings.ContainsRune("._-", r) && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return invalid("invalid_argument", "kind_prefix holds lowercase letters, digits, dots, underscores and hyphens")
		}
	}
	return nil
}

func (m *management) getSpendForecast(ctx context.Context, a forecastArgs) (any, error) {
	if a.Month != "" {
		if _, err := time.Parse("2006-01", a.Month); err != nil {
			return nil, invalid("invalid_argument", "month must be YYYY-MM")
		}
	}
	if m.opts.AllowedApps != nil {
		return nil, invalid("app_access_denied", "the account forecast needs a connection to every app; use gregale_get_costs for one app")
	}
	out, err := m.client.GetFinancialForecast(ctx, a.Month)
	if err != nil {
		return nil, err
	}
	return compactForecast(out), nil
}

// spendForecast is the account forecast without its per-usage allocation
// evidence. Allocations were over 300 KB on an ordinary account, far past the
// MCP result bound, so the tool failed with response_too_large.
type spendForecast struct {
	api.FinancialForecastResponse
	AllocationsOmitted int    `json:"allocations_omitted"`
	Note               string `json:"note,omitempty"`
}

func compactForecast(f api.FinancialForecastResponse) spendForecast {
	omitted := 0
	for i := range f.Meters {
		for j := range f.Meters[i].Accrued.Contracts {
			omitted += len(f.Meters[i].Accrued.Contracts[j].Allocations)
			f.Meters[i].Accrued.Contracts[j].Allocations = nil
		}
	}
	out := spendForecast{FinancialForecastResponse: f, AllocationsOmitted: omitted}
	if omitted > 0 {
		out.Note = "Per-usage allocation evidence is omitted; read one app's costs with gregale_get_costs."
	}
	return out
}

// budgetVisible reports whether this connection may see and plan the
// budget: account budgets need every app; app budgets need that app.
func (m *management) budgetVisible(scope financial.BudgetScope) bool {
	switch scope.Kind {
	case "account":
		return m.opts.AllowedApps == nil
	case "app":
		return m.appIDAllowed(scope.ID)
	}
	return m.opts.AllowedApps == nil
}

func (m *management) listBudgets(ctx context.Context, _ struct{}) (any, error) {
	list, err := m.client.ListFinancialBudgets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.FinancialBudgetResponse, 0, len(list.Budgets))
	for _, b := range list.Budgets {
		if m.budgetVisible(b.Spec.Scope) {
			out = append(out, b)
		}
	}
	return map[string]any{"budgets": out}, nil
}

// notifyOnly reports whether a budget only notifies.
func notifyOnly(s financial.BudgetSpec) bool {
	return s.Action == "notify" && s.Mode == "monitored"
}

func (m *management) planBudgetChange(ctx context.Context, a planBudgetArgs) (any, error) {
	if err := validateReason("budget", a.Reason); err != nil {
		return nil, err
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	p := &budgetPlan{Op: a.Op, AccountID: account.ID, Reason: a.Reason, ExpiresAt: time.Now().Add(budgetPlanTTL)}
	switch a.Op {
	case "create":
		if a.BudgetID != "" {
			return nil, invalid("invalid_argument", "create takes no budget_id")
		}
		scope, err := m.budgetScope(ctx, a)
		if err != nil {
			return nil, err
		}
		p.Spec = financial.BudgetSpec{Scope: scope, Basis: "net_usage", Meters: []string{}, Enabled: true}
		p.Effect = "Adds a budget that notifies the account's channels as spend passes each level. Nothing is blocked or stopped."
	case "update":
		if err := validateSegment(a.BudgetID, "budget_id"); err != nil {
			return nil, err
		}
		if a.Scope != "" || a.App != "" {
			return nil, invalid("invalid_argument", "update keeps the budget's scope; it takes no scope or app")
		}
		current, err := m.client.GetFinancialBudget(ctx, a.BudgetID)
		if err != nil {
			return nil, err
		}
		if current.ID != a.BudgetID || current.DeletedAt != nil || !m.budgetVisible(current.Spec.Scope) {
			return nil, invalid("app_access_denied", "the budget is outside this connection")
		}
		if !notifyOnly(current.Spec) {
			return nil, invalid("approval_required", "the budget enforces spend; people change enforcing budgets")
		}
		before := current.Spec
		p.BudgetID, p.ExpectedRevision, p.Before = a.BudgetID, current.Revision, &before
		// Start from the budget as it is and change only the fields given.
		p.Spec = current.Spec
		p.Spec.Meters = slices.Clone(current.Spec.Meters)
		p.Spec.NotifyMillicents = slices.Clone(current.Spec.NotifyMillicents)
		p.Effect = "Changes only the given fields of the budget. Nothing is blocked or stopped."
	default:
		return nil, invalid("invalid_argument", "op must be create or update")
	}
	overlayBudget(&p.Spec, a)
	if err := validNotifyBudget(p.Spec); err != nil {
		return nil, err
	}
	preview, err := m.client.PreviewFinancialBudget(ctx, p.Spec)
	if err != nil {
		return nil, err
	}
	p.Preview = preview
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, old := range m.budgetPlans {
		if now.After(old.ExpiresAt) {
			delete(m.budgetPlans, id)
		}
	}
	if len(m.budgetPlans) >= maxBudgetPlans {
		return nil, invalid("plan_capacity", "connection has too many active budget plans; wait for expiration")
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	p.ID = hex.EncodeToString(id)
	m.budgetPlans[p.ID] = p
	if err := m.persistMemoryPlansLocked(); err != nil {
		delete(m.budgetPlans, p.ID)
		return nil, err
	}
	return *p, nil
}

func (m *management) applyBudgetChange(ctx context.Context, a applyArgs) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.budgetPlans[a.PlanID]
	if p == nil {
		return nil, invalid("invalid_plan", "unknown or expired budget plan; review a new one")
	}
	if p.receipt != nil {
		return p.receipt, nil
	}
	if time.Now().After(p.ExpiresAt) {
		delete(m.budgetPlans, a.PlanID)
		return nil, invalid("plan_expired", "plan expired; review a new one")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, invalid("account_changed", "account identity changed; create a new connection and plan")
	}
	ctx = api.ContextWithIdempotencyKey(ctx, "mcp-budget-"+p.ID)
	var out api.FinancialBudgetResponse
	if p.Op == "create" {
		out, err = m.client.CreateFinancialBudget(ctx, api.CreateFinancialBudgetRequest{Spec: p.Spec})
	} else {
		out, err = m.client.UpdateFinancialBudget(ctx, p.BudgetID, api.UpdateFinancialBudgetRequest{ExpectedRevision: p.ExpectedRevision, Spec: p.Spec})
	}
	if err != nil {
		return nil, err
	}
	p.receipt = map[string]any{"plan_id": p.ID, "op": p.Op, "budget": out}
	// The write succeeded under its idempotency key; a failed journal only
	// costs the replayed receipt.
	_ = m.persistMemoryPlansLocked()
	return p.receipt, nil
}

// budgetScope resolves a new budget's scope: an app of this connection, or
// the account on a connection to every app.
func (m *management) budgetScope(ctx context.Context, a planBudgetArgs) (financial.BudgetScope, error) {
	switch a.Scope {
	case "", "app":
		app, err := m.consentedApp(ctx, a.App, false)
		if err != nil {
			return financial.BudgetScope{}, err
		}
		return financial.BudgetScope{Kind: "app", ID: app.ID}, nil
	case "account":
		if a.App != "" || m.opts.AllowedApps != nil {
			return financial.BudgetScope{}, invalid("app_access_denied", "account budgets need a connection to every app and take no app")
		}
		return financial.BudgetScope{Kind: "account"}, nil
	}
	return financial.BudgetScope{}, invalid("invalid_argument", "scope must be account or app")
}

// overlayBudget sets the fields the agent gave, and always keeps the budget
// notify-only.
func overlayBudget(s *financial.BudgetSpec, a planBudgetArgs) {
	if a.Name != "" {
		s.Name = a.Name
	}
	if a.Currency != "" {
		s.Currency = a.Currency
	}
	if a.Meters != nil {
		s.Meters = slices.Clone(a.Meters)
	}
	if a.Basis != "" {
		s.Basis = a.Basis
	}
	if a.LimitMillicents != 0 {
		s.LimitMillicents = a.LimitMillicents
	}
	if a.NotifyMillicents != nil {
		s.NotifyMillicents = slices.Clone(a.NotifyMillicents)
	}
	if a.Enabled != nil {
		s.Enabled = *a.Enabled
	}
	s.Mode, s.Action = "monitored", "notify"
	if s.ResumeRule == "" {
		s.ResumeRule = "manual"
	}
}

func validNotifyBudget(s financial.BudgetSpec) error {
	if s.Name == "" || s.Currency == "" || s.LimitMillicents <= 0 || !slices.Contains(financial.BudgetBases, s.Basis) {
		return invalid("invalid_argument", "a budget needs name, currency, a positive limit_millicents and basis net_usage or gross_usage")
	}
	for _, n := range s.NotifyMillicents {
		if n <= 0 {
			return invalid("invalid_argument", "notify_millicents levels must be positive")
		}
	}
	return nil
}
