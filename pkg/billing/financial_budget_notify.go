package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// BudgetMailer is the slice of mail.Sender the budget notifier needs.
type BudgetMailer interface {
	Send(ctx context.Context, msg mail.Message) error
}

// BudgetNotifier sends ADR-1500 budget threshold emails. meterd runs it on a
// timer. Each pass claims the thresholds that known spending has crossed,
// then delivers the pending claims.
type BudgetNotifier struct {
	store   state.Store
	mailer  BudgetMailer
	log     *slog.Logger
	now     func() time.Time
	outcome *prometheus.CounterVec
}

// NewBudgetNotifier wires a notifier. reg may be nil (no metrics).
func NewBudgetNotifier(store state.Store, mailer BudgetMailer, log *slog.Logger, now func() time.Time, reg prometheus.Registerer) *BudgetNotifier {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	n := &BudgetNotifier{store: store, mailer: mailer, log: log, now: now}
	n.outcome = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meterd_financial_budget_notifications_total",
		Help: "ADR-1500 budget notification delivery outcomes: sent, failed (retried), abandoned (out of attempts) and cancelled (budget disabled or deleted first).",
	}, []string{"outcome"})
	if reg != nil {
		reg.MustRegister(n.outcome)
	}
	return n
}

// Run evaluates every interval until ctx ends. A failed pass is logged and
// retried on the next tick.
func (n *BudgetNotifier) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = api.FinancialBudgetNotifyInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := n.RunOnce(ctx); err != nil && ctx.Err() == nil {
			n.log.Warn("financial budget notifications", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce claims newly crossed thresholds for the current UTC month, then
// delivers every pending claim.
func (n *BudgetNotifier) RunOnce(ctx context.Context) error {
	store, ok := n.store.(state.FinancialBudgetNotificationStore)
	if !ok || n.mailer == nil {
		return nil
	}
	now := n.now().UTC()
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	policies, err := store.ListFinancialBudgetNotifyPolicies(ctx, api.FinancialBudgetNotifyBatch)
	if err != nil {
		return fmt.Errorf("financial budget notify policies: %w", err)
	}
	var errs []error
	for start := 0; start < len(policies); {
		end := start + 1
		for end < len(policies) && policies[end].AccountID == policies[start].AccountID {
			end++
		}
		errs = append(errs, n.claimAccount(ctx, store, policies[start:end], period, now))
		start = end
	}
	errs = append(errs, n.deliver(ctx, store))
	return errors.Join(errs...)
}

func (n *BudgetNotifier) claimAccount(ctx context.Context, store state.FinancialBudgetNotificationStore, policies []state.FinancialBudget, period, now time.Time) error {
	account := policies[0].AccountID
	report, err := ReadFinancialCosts(ctx, n.store, account, period, now)
	if err != nil {
		return fmt.Errorf("financial budget costs for account %s: %w", account, err)
	}
	var errs []error
	for _, p := range policies {
		known, err := financialBudgetKnown(p.Spec, report)
		if err != nil {
			errs = append(errs, fmt.Errorf("financial budget %s amount: %w", p.ID, err))
			continue
		}
		for _, threshold := range financialBudgetThresholds(p.Spec) {
			// A $0 threshold means "any spending": it needs a positive amount.
			if known.millicents < threshold || known.millicents == 0 {
				break
			}
			if _, err := store.ClaimFinancialBudgetNotification(ctx, state.FinancialBudgetNotification{
				AccountID: account, PolicyID: p.ID, PeriodStart: period, ThresholdMillicents: threshold,
				PolicyRevision: p.Revision, KnownMillicents: known.millicents, CoverageComplete: known.complete,
			}); err != nil {
				errs = append(errs, fmt.Errorf("financial budget %s claim: %w", p.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// financialBudgetThresholds is notify_millicents plus the limit, ascending and
// without duplicates.
func financialBudgetThresholds(spec financial.BudgetSpec) []int64 {
	out := append(append([]int64{}, spec.NotifyMillicents...), spec.LimitMillicents)
	slices.Sort(out)
	return slices.Compact(out)
}

type budgetNotificationGroup struct {
	accountID, policyID string
	period              time.Time
	highest             state.FinancialBudgetNotification
	attempts            int
}

func (n *BudgetNotifier) deliver(ctx context.Context, store state.FinancialBudgetNotificationStore) error {
	pending, err := store.ListPendingFinancialBudgetNotifications(ctx, api.FinancialBudgetNotifyBatch)
	if err != nil {
		return fmt.Errorf("financial budget pending notifications: %w", err)
	}
	var groups []*budgetNotificationGroup
	byKey := map[string]*budgetNotificationGroup{}
	for _, row := range pending {
		key := row.PolicyID + "/" + row.PeriodStart.Format(time.RFC3339)
		g, ok := byKey[key]
		if !ok {
			g = &budgetNotificationGroup{accountID: row.AccountID, policyID: row.PolicyID, period: row.PeriodStart, highest: row}
			byKey[key] = g
			groups = append(groups, g)
		}
		if row.ThresholdMillicents > g.highest.ThresholdMillicents {
			g.highest = row
		}
		g.attempts = max(g.attempts, row.Attempts)
	}
	var errs []error
	for _, g := range groups {
		errs = append(errs, n.deliverGroup(ctx, store, g))
	}
	return errors.Join(errs...)
}

func (n *BudgetNotifier) deliverGroup(ctx context.Context, store state.FinancialBudgetNotificationStore, g *budgetNotificationGroup) error {
	budgets, ok := n.store.(state.FinancialBudgetStore)
	if !ok {
		return ErrFinancialUnavailable
	}
	policy, err := budgets.GetFinancialBudget(ctx, g.accountID, g.policyID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("financial budget %s: %w", g.policyID, err)
	}
	if err != nil || policy.DeletedAt != nil || !policy.Spec.Enabled || !FinancialBudgetResponseIntegrated(policy.Spec) {
		return n.record(ctx, store, g, state.FinancialBudgetNotificationCancelled, "budget disabled or deleted before delivery")
	}
	acct, err := n.store.AccountByID(ctx, g.accountID)
	if errors.Is(err, state.ErrNotFound) {
		return n.record(ctx, store, g, state.FinancialBudgetNotificationCancelled, "account not found")
	}
	if err != nil {
		return fmt.Errorf("financial budget %s account: %w", g.policyID, err)
	}
	subject, body := mail.BudgetThresholdBody(mail.BudgetThreshold{
		BudgetName:          policy.Spec.Name,
		Basis:               policy.Spec.Basis,
		ThresholdMillicents: g.highest.ThresholdMillicents,
		KnownMillicents:     g.highest.KnownMillicents,
		LimitMillicents:     policy.Spec.LimitMillicents,
		PeriodStart:         g.period,
		CoverageComplete:    g.highest.CoverageComplete,
	})
	sendErr := n.mailer.Send(ctx, mail.Message{
		To:        []string{acct.Email},
		Subject:   subject,
		TextBody:  body,
		MessageID: budgetNotificationMessageID(g),
	})
	if sendErr == nil {
		return n.record(ctx, store, g, state.FinancialBudgetNotificationSent, "")
	}
	outcome := state.FinancialBudgetNotificationFailed
	if g.attempts+1 >= api.FinancialBudgetNotifyMaxAttempts {
		outcome = state.FinancialBudgetNotificationAbandoned
	}
	return errors.Join(fmt.Errorf("financial budget %s email: %w", g.policyID, sendErr),
		n.record(ctx, store, g, outcome, sendErr.Error()))
}

func (n *BudgetNotifier) record(ctx context.Context, store state.FinancialBudgetNotificationStore, g *budgetNotificationGroup, outcome, lastError string) error {
	if len(lastError) > api.FinancialBudgetNotifyErrorBytes {
		lastError = strings.ToValidUTF8(lastError[:api.FinancialBudgetNotifyErrorBytes], "")
	}
	if _, err := store.RecordFinancialBudgetNotificationOutcome(ctx, g.policyID, g.period, g.highest.ThresholdMillicents, outcome, lastError); err != nil {
		return fmt.Errorf("financial budget %s outcome %s: %w", g.policyID, outcome, err)
	}
	n.outcome.WithLabelValues(outcome).Inc()
	return nil
}

// budgetNotificationMessageID is the mail provider's idempotency key: a retry
// of the same email inside the provider's replay window is not sent twice.
func budgetNotificationMessageID(g *budgetNotificationGroup) string {
	return "budget:" + g.policyID + ":" + g.period.Format("2006-01") + ":" + strconv.FormatInt(g.highest.ThresholdMillicents, 10)
}
