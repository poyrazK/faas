// adr: 566 — monetary reservations are serialized with policy intent.
package state

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// FinancialEgressReadinessStore supplies a read-only activation preflight.
type FinancialEgressReadinessStore interface {
	FinancialEgressReady(context.Context, string) (bool, error)
}

// FinancialComputeStore is internal execution authority. Customer APIs cannot
// mint, renew or settle native funding receipts.
type FinancialComputeStore interface {
	CancelFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	RegisterFinancialComputeOwner(context.Context, string, string) error
	PrepareFinancialJobCompute(context.Context, string, string) error
	DeferFinancialJobClaim(context.Context, string, int, string, string, time.Time) error
	FinancialComputeOwnerValid(context.Context, string, string) (bool, error)
	FinancialComputeRetired(context.Context, string, string) (bool, error)
	FinancialComputeLease(context.Context, string, string) (financial.ComputeLease, bool, error)
	FinancialComputeInstanceLease(context.Context, string, string, string) (financial.ComputeLease, bool, error)
	AcquireFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	RenewFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	SettleFinancialCompute(context.Context, financial.ComputeRequest) (financial.ComputeLease, error)
	FinancialComputeNodeLeases(context.Context, string) ([]financial.ComputeLease, error)
	FinancialComputeBalance(context.Context, FinancialBudget, time.Time) (financial.ComputeBalance, error)
	FinancialComputeReady(context.Context, string, string) (bool, error)
	FinancialComputePolicies(context.Context, string, string) ([]FinancialBudget, error)
	FinancialComputeBillingSeconds(context.Context, string, string, string, time.Time, int64) (int64, error)
	FinancialComputeFloorCovered(context.Context, string, string, time.Time) (bool, error)
	FinancialComputeAppManaged(context.Context, string, string) (bool, error)
}

type financialComputeHold struct {
	PolicyID    string
	Revision    int64
	PeriodStart time.Time
}

// Each adapter runs this logic under the same account lock as policy mutation.
// Database time is authoritative; callers never select deadlines or money.
type financialComputeTxn interface {
	clock() time.Time
	book(string, time.Time) (financial.ComputeBook, error)
	putBook(financial.ComputeBook) error
	lease(string) (financial.ComputeLease, error)
	putLease(financial.ComputeLease) error
	active(string) ([]financial.ComputeLease, error)
	policies(string) ([]FinancialBudget, error)
	holds() ([]financialComputeHold, error)
	putHold(financialComputeHold) error
	owner(string, string) (bool, error)
	jobCurrent(financial.ComputeLease) (bool, error)
	allowance(time.Time) (int64, error)
	egressPrices(time.Time) ([]FinancialPriceSnapshot, error)
	egressOwner(string, string) (bool, error)
	// workloadProject is an app's current project ("" for jobs and apps
	// outside any project; ADR-1750, amendment 1).
	workloadProject(string) (string, error)
}

func validateComputeRequest(r financial.ComputeRequest, mutate bool) bool {
	if uuid.Validate(r.LeaseID) != nil || uuid.Validate(r.Fence) != nil || uuid.Validate(r.RequestID) != nil || uuid.Validate(r.AccountID) != nil || (r.JobID == "" && (uuid.Validate(r.WorkloadKey()) != nil || r.JobRunID != "")) || (r.JobID != "" && (r.AppID != "" || uuid.Validate(r.JobID) != nil || uuid.Validate(r.JobRunID) != nil || r.JobTaskIndex < 0)) || uuid.Validate(r.NodeID) != nil || uuid.Validate(r.Incarnation) != nil || r.InstanceID == "" || len(r.InstanceID) > api.FinancialComputeInstanceBytes {
		return false
	}
	return !mutate || r.Revision > 0
}

func computeLeaseRequest(l financial.ComputeLease, r financial.ComputeRequest) bool {
	return l.ID == r.LeaseID && l.Fence == r.Fence && l.AccountID == r.AccountID && l.AppID == r.AppID && l.JobID == r.JobID && l.JobRunID == r.JobRunID && l.JobTaskIndex == r.JobTaskIndex && l.InstanceID == r.InstanceID && l.NodeID == r.NodeID && l.Incarnation == r.Incarnation
}

func strictComputePolicies(rows []FinancialBudget, app string) []FinancialBudget {
	out := []FinancialBudget{}
	for _, p := range rows {
		if p.DeletedAt == nil && p.Spec.Enabled && (financial.StrictAccountCompute(p.Spec) || financial.ComputeScopeKey(p.Spec.Scope) == app && (financial.StrictAppCompute(p.Spec) || financial.StrictJobCompute(p.Spec))) {
			out = append(out, p)
		}
	}
	return out
}

func validateComputePolicyFunding(b financial.ComputeBook, readErr error, continuing bool, limit int64) error {
	// An existing policy may release its hold before the next month's book
	// exists. Editing intent cannot issue funding: acquire still joins old
	// physical leases and verifies accounting before initializing that book.
	if continuing && errors.Is(readErr, ErrNotFound) {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	if b.Committed+b.Reserved > limit {
		return financial.ErrComputeBudget
	}
	return nil
}

func computeAcquire(tx financialComputeTxn, r financial.ComputeRequest) (financial.ComputeLease, error) {
	var bindErr error
	r, bindErr = financial.BindComputeRequest(r)
	if bindErr != nil {
		return financial.ComputeLease{}, bindErr
	}
	if !validateComputeRequest(r, false) || r.Revision != 0 || r.BillableMB <= 0 || r.BillableMB > api.FinancialComputeRAMMaxMB {
		return financial.ComputeLease{}, ErrInvalidArgument
	}
	if r.EgressRefill {
		return financial.ComputeLease{}, ErrInvalidArgument
	}
	if old, err := tx.lease(r.LeaseID); err == nil {
		if old.Denied && old.DenialReason == "cancelled_before_grant" {
			return financial.ComputeLease{}, financial.ErrComputeFence
		}
		if computeLeaseRequest(old, r) && old.RequestID == r.RequestID && old.Denied && old.BillableMB == r.BillableMB {
			if old.DenialReason == "attempt_identity_missing" {
				return old, errors.Join(financial.ErrComputeBudget, financial.ErrComputeUnavailable)
			}
			return old, financial.ErrComputeBudget
		}
		if computeLeaseRequest(old, r) && old.RequestID == r.RequestID && old.RequestOperation == "acquire" && old.BillableMB == r.BillableMB && old.SettledAt == nil {
			return old, nil
		}
		return financial.ComputeLease{}, financial.ErrComputeFence
	} else if !errors.Is(err, ErrNotFound) {
		return financial.ComputeLease{}, err
	}
	valid, err := tx.owner(r.NodeID, r.Incarnation)
	if err != nil || !valid {
		return financial.ComputeLease{}, errors.Join(financial.ErrComputeUnavailable, err)
	}
	active, err := tx.active(r.WorkloadKey())
	if err != nil {
		return financial.ComputeLease{}, err
	}
	for _, l := range active {
		if l.InstanceID == r.InstanceID && l.NodeID == r.NodeID {
			return financial.ComputeLease{}, ErrConflict
		}
	}
	project, policies, err := computeWorkloadStrictPolicies(tx, r.WorkloadKey())
	if err != nil {
		return financial.ComputeLease{}, err
	}
	if len(policies) > 0 {
		for _, old := range active {
			if old.InstanceID == r.InstanceID {
				return financial.ComputeLease{}, ErrConflict
			}
		}
	}
	now := tx.clock().UTC().Truncate(time.Second)
	l := financial.ComputeLease{ID: r.LeaseID, Fence: r.Fence, RequestID: r.RequestID, Revision: 1, RequestOperation: "acquire", RateMillicents: api.OverageMillicentsPerGBHour, RateQuantity: api.SecondsPerGBHour, AccountID: r.AccountID, AppID: r.AppID, JobID: r.JobID, JobRunID: r.JobRunID, JobTaskIndex: r.JobTaskIndex, InstanceID: r.InstanceID, NodeID: r.NodeID, Incarnation: r.Incarnation, BillableMB: r.BillableMB, PeriodStart: financial.ComputeMonth(now), StartedAt: now, GrantedAt: tx.clock(), RunUntil: now.Add(api.FinancialComputeLeaseDuration), FundedUntil: now.Add(api.FinancialComputeLeaseDuration + api.FinancialComputeTeardownReserve), Strict: len(policies) > 0}
	l.AttemptID = r.AttemptID
	l.Fenced = r.Fenced
	// Only funding needs a persisted attempt identity. An unbudgeted boot is
	// still registered for activation, but a legacy path without a wake ID
	// must not fail for customers who never set a strict cap.
	if !r.Fenced && l.Strict {
		l.Denied = true
		l.DenialReason = "attempt_identity_missing"
		l.RunUntil = l.StartedAt
		l.FundedUntil = l.StartedAt
		l.SettledAt = &l.StartedAt
		l.RequestOperation = "deny"
		if err := tx.putLease(l); err != nil {
			return financial.ComputeLease{}, err
		}
		return l, errors.Join(financial.ErrComputeBudget, financial.ErrComputeUnavailable)
	}
	if !l.Strict {
		return l, tx.putLease(l)
	}
	for _, old := range active {
		if !old.Strict || old.PeriodStart != l.PeriodStart {
			return financial.ComputeLease{}, financial.ErrComputeUnavailable
		}
	}
	b, err := tx.book(r.WorkloadKey(), l.PeriodStart)
	if err != nil {
		return financial.ComputeLease{}, errors.Join(financial.ErrComputeUnavailable, err)
	}
	account, err := computeAccountFundingBook(tx, &l, policies)
	if err != nil {
		return financial.ComputeLease{}, err
	}
	projectBook, err := computeProjectFundingBook(tx, &l, project, policies)
	if err != nil {
		return financial.ComputeLease{}, err
	}
	if err = computeFund(tx, &b, account, projectBook, &l, policies, true); err != nil {
		if errors.Is(err, financial.ErrComputeBudget) {
			// This committed negative receipt authorizes a fresh native
			// absence audit after a lost reply or daemon restart. It grants
			// no execution window and never frees another attempt's money.
			l.Denied = true
			l.QuantityCommitted, l.QuantityReserved = 0, 0
			l.EgressGranted, l.EgressGrant, l.EgressSequence = 0, 0, 0
			l.ProjectID = ""
			l.RunUntil = l.StartedAt
			l.FundedUntil = l.StartedAt
			l.SettledAt = &l.StartedAt
			l.RequestOperation = "deny"
			if writeErr := tx.putLease(l); writeErr != nil {
				return financial.ComputeLease{}, writeErr
			}
			return l, err
		}
		return financial.ComputeLease{}, err
	}
	if err = tx.putBook(b); err != nil {
		return financial.ComputeLease{}, err
	}
	if account != nil {
		if err = tx.putBook(*account); err != nil {
			return financial.ComputeLease{}, err
		}
	}
	if projectBook != nil {
		if err = tx.putBook(*projectBook); err != nil {
			return financial.ComputeLease{}, err
		}
	}
	return l, tx.putLease(l)
}

// Caller holds the node's destruction join and has freshly audited physical
// absence. Persist cancellation before acknowledging it, fencing late creates.
func computeCancel(tx financialComputeTxn, r financial.ComputeRequest) (financial.ComputeLease, error) {
	var err error
	r, err = financial.BindComputeRequest(r)
	if err != nil {
		return financial.ComputeLease{}, err
	}
	old, err := tx.lease(r.LeaseID)
	if err == nil {
		if old.NodeID != r.NodeID || old.AppID != r.AppID || old.JobID != r.JobID || old.JobRunID != r.JobRunID || old.JobTaskIndex != r.JobTaskIndex || old.InstanceID != r.InstanceID {
			return financial.ComputeLease{}, financial.ErrComputeFence
		}
		if old.SettledAt != nil {
			return old, nil
		}
		request := financial.ComputeRequest{LeaseID: old.ID, Fence: old.Fence, RequestID: uuid.NewString(), Revision: old.Revision, AccountID: old.AccountID, AppID: old.AppID, JobID: old.JobID, JobRunID: old.JobRunID, JobTaskIndex: old.JobTaskIndex, InstanceID: old.InstanceID, NodeID: old.NodeID, Incarnation: old.Incarnation}
		return computeMutate(tx, request, true)
	}
	if !errors.Is(err, ErrNotFound) {
		return financial.ComputeLease{}, err
	}
	valid, err := tx.owner(r.NodeID, r.Incarnation)
	if err != nil || !valid {
		return financial.ComputeLease{}, financial.ErrComputeFence
	}
	now := tx.clock().UTC()
	l := financial.ComputeLease{ID: r.LeaseID, Fence: r.Fence, RequestID: r.RequestID, RequestOperation: "cancel", Revision: 1, AccountID: r.AccountID, AppID: r.AppID, JobID: r.JobID, JobRunID: r.JobRunID, JobTaskIndex: r.JobTaskIndex, InstanceID: r.InstanceID, NodeID: r.NodeID, Incarnation: r.Incarnation, AttemptID: r.AttemptID, Fenced: true, Strict: true, Denied: true, DenialReason: "cancelled_before_grant", BillableMB: 1, RateMillicents: api.OverageMillicentsPerGBHour, RateQuantity: api.SecondsPerGBHour, PeriodStart: financial.ComputeMonth(now), StartedAt: now, GrantedAt: now, RunUntil: now, FundedUntil: now, SettledAt: &now}
	return l, tx.putLease(l)
}

func computeFund(tx financialComputeTxn, b, account, project *financial.ComputeBook, l *financial.ComputeLease, policies []FinancialBudget, refill bool) error {
	if l.FundedUntil.After(b.PeriodEnd) {
		l.FundedUntil = b.PeriodEnd
		l.RunUntil = b.PeriodEnd.Add(-api.FinancialComputeTeardownReserve)
	}
	if !tx.clock().Add(api.FinancialComputeRenewMargin).Before(l.RunUntil) {
		return financial.ErrComputeBudget
	}
	total, err := financial.ComputeCharge(l.BillableMB, l.StartedAt, l.FundedUntil, l.RateMillicents, l.RateQuantity)
	if err != nil {
		return err
	}
	// Subtract before adding: neither an int64 wrap nor a correction can
	// create authority. Old funding stays committed during a renewal.
	without := b.Reserved - l.Reserved
	if without < 0 || total < l.Committed {
		return financial.ErrComputeUnavailable
	}
	wanted := total - l.Committed
	if wanted > api.FinancialBudgetMoneyMax-b.Committed || without > api.FinancialBudgetMoneyMax-b.Committed-wanted {
		return financial.ErrComputeBudget
	}
	b.Reserved = without + wanted
	appErr := computeCheckPolicies(tx, *b, policies)
	var accountErr, projectErr error
	if account != nil {
		accountErr = computeFundAccount(tx, account, l, policies, refill)
	}
	if project != nil {
		projectErr = computeFundProject(tx, project, l, wanted, policies)
	}
	if err := errors.Join(appErr, accountErr, projectErr); err != nil {
		return err
	}
	l.Reserved = wanted
	return nil
}

func computeMutate(tx financialComputeTxn, r financial.ComputeRequest, settle bool) (financial.ComputeLease, error) {
	if !validateComputeRequest(r, true) {
		return financial.ComputeLease{}, ErrInvalidArgument
	}
	l, err := tx.lease(r.LeaseID)
	if err != nil {
		return l, err
	}
	if !computeLeaseRequest(l, r) {
		return financial.ComputeLease{}, financial.ErrComputeFence
	}
	operation := "renew"
	if settle {
		operation = "settle"
	}
	if l.RequestID == r.RequestID {
		if l.RequestOperation != operation || l.RequestRevision != r.Revision || l.RequestEgressRefill != r.EgressRefill {
			return financial.ComputeLease{}, financial.ErrComputeFence
		}
		return l, nil
	}
	if l.Revision != r.Revision || l.SettledAt != nil {
		return financial.ComputeLease{}, financial.ErrComputeFence
	}
	now := tx.clock().UTC()
	if !settle && l.JobID != "" {
		valid, err := tx.jobCurrent(l)
		if err != nil || !valid {
			return financial.ComputeLease{}, errors.Join(financial.ErrComputeFence, err)
		}
	}
	if !settle {
		valid, err := tx.owner(r.NodeID, r.Incarnation)
		if err != nil || !valid {
			return financial.ComputeLease{}, financial.ErrComputeFence
		}
	}
	if !settle && !now.Add(api.FinancialComputeRenewMargin).Before(l.RunUntil) {
		return financial.ComputeLease{}, financial.ErrComputeBudget
	}
	if l.Strict {
		account, err := computeProgressAccount(tx, &l, minComputeTime(now, l.FundedUntil), settle)
		if err != nil {
			return l, err
		}
		b, err := tx.book(l.WorkloadKey(), l.PeriodStart)
		if err != nil {
			return l, err
		}
		until := minComputeTime(now, l.FundedUntil)
		spent, err := financial.ComputeCharge(l.BillableMB, l.StartedAt, until, l.RateMillicents, l.RateQuantity)
		if err != nil {
			return l, err
		}
		delta := spent - l.Committed
		if delta < 0 || delta > l.Reserved || b.Reserved < delta {
			return l, financial.ErrComputeUnavailable
		}
		// The project book moves by the same gross amounts as the workload book.
		projectBook, err := computeLeaseProjectBook(tx, l)
		if err != nil {
			return l, err
		}
		if projectBook != nil {
			if projectBook.Reserved < delta {
				return l, financial.ErrComputeUnavailable
			}
			projectBook.Committed += delta
			projectBook.Reserved -= delta
		}
		b.Committed += delta
		b.Reserved -= delta
		l.Committed = spent
		l.Reserved -= delta
		if settle {
			b.Reserved -= l.Reserved
			if projectBook != nil {
				if projectBook.Reserved < l.Reserved {
					return l, financial.ErrComputeUnavailable
				}
				projectBook.Reserved -= l.Reserved
			}
			l.Reserved = 0
		} else {
			project, policies, err := computeWorkloadStrictPolicies(tx, l.WorkloadKey())
			if err != nil {
				return l, err
			}
			// An app moved into a capped project since this attempt began
			// cannot keep running outside that project's book.
			if project != l.ProjectID && slices.ContainsFunc(policies, func(p FinancialBudget) bool { return financial.StrictProjectCompute(p.Spec) }) {
				return financial.ComputeLease{}, financial.ErrComputeUnavailable
			}
			if projectBook != nil && project != l.ProjectID {
				// The app left the project that funds this attempt; that
				// project's cap still bounds it until it settles.
				leasePolicies, err := computeProjectPolicies(tx, l.ProjectID)
				if err != nil {
					return l, err
				}
				policies = append(policies, leasePolicies...)
			}
			l.RunUntil = now.Add(api.FinancialComputeLeaseDuration).Truncate(time.Second)
			l.FundedUntil = l.RunUntil.Add(api.FinancialComputeTeardownReserve)
			if err = computeFund(tx, &b, account, projectBook, &l, policies, r.EgressRefill); err != nil {
				return financial.ComputeLease{}, err
			}
		}
		if err = tx.putBook(b); err != nil {
			return l, err
		}
		if account != nil {
			if err = tx.putBook(*account); err != nil {
				return l, err
			}
		}
		if projectBook != nil {
			if err = tx.putBook(*projectBook); err != nil {
				return l, err
			}
		}
	} else if !settle {
		_, policies, err := computeWorkloadStrictPolicies(tx, l.WorkloadKey())
		if err != nil {
			return l, err
		}
		if len(policies) > 0 {
			return financial.ComputeLease{}, financial.ErrComputeUnavailable
		}
		l.RunUntil = now.Add(api.FinancialComputeLeaseDuration)
		l.FundedUntil = l.RunUntil
	}
	if settle {
		l.SettledAt = &now
	}
	l.RequestOperation = operation
	l.RequestEgressRefill = r.EgressRefill
	l.RequestRevision = r.Revision
	l.Revision++
	l.RequestID = r.RequestID
	l.GrantedAt = now
	return l, tx.putLease(l)
}

func minComputeTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func computeBalance(tx financialComputeTxn, p FinancialBudget, now time.Time) (financial.ComputeBalance, error) {
	month := financial.ComputeMonth(now)
	out := financial.ComputeBalance{AccountID: p.AccountID, ScopeKind: p.Spec.Scope.Kind, Basis: p.Spec.Basis, AppID: p.Spec.Scope.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), ObservedAt: tx.clock()}
	switch p.Spec.Scope.Kind {
	case "job":
		out.JobID, out.AppID = p.Spec.Scope.ID, ""
	case "project":
		out.ProjectID, out.AppID = p.Spec.Scope.ID, ""
	}
	key := financial.ComputeScopeKey(p.Spec.Scope)
	b, err := tx.book(key, month)
	if errors.Is(err, ErrNotFound) {
		out.RemainingMillicents = p.Spec.LimitMillicents
	} else if err != nil {
		return out, err
	} else {
		out.Initialized = true
	}
	out.CommittedMillicents, out.ReservedMillicents = b.Committed, b.Reserved
	if out.ScopeKind == "account" {
		out.AllowanceGranted = b.AllowanceGranted
		out.AllowanceCommitted = min(b.AllowanceGranted, b.QuantityCommitted)
		out.AllowanceReserved = min(b.AllowanceGranted-out.AllowanceCommitted, b.QuantityReserved)
		out.AllowanceRemaining = b.AllowanceGranted - out.AllowanceCommitted - out.AllowanceReserved
	}
	if financial.StrictAccountEgress(p.Spec) {
		e := b.Egress
		out.Initialized = out.Initialized && e.Initialized
		out.Egress = &financial.EgressBalance{Initialized: e.Initialized, CommittedBytes: e.QuantityCommitted, ReservedBytes: e.QuantityReserved, AllowanceGranted: e.AllowanceGranted, AllowanceCommitted: min(e.AllowanceGranted, e.QuantityCommitted), CommittedMillicents: e.Committed, ReservedMillicents: e.Reserved}
		out.Egress.AllowanceReserved = min(e.AllowanceGranted-out.Egress.AllowanceCommitted, e.QuantityReserved)
		out.Egress.AllowanceRemaining = e.AllowanceGranted - out.Egress.AllowanceCommitted - out.Egress.AllowanceReserved
		out.CommittedMillicents, out.ReservedMillicents, err = computePolicyMoney(b, p.Spec)
		if err != nil {
			return out, err
		}
	}
	out.RemainingMillicents = max(int64(0), p.Spec.LimitMillicents-out.CommittedMillicents-out.ReservedMillicents)
	active, err := tx.active(key)
	if err != nil {
		return out, err
	}
	out.UnconfirmedLeases = len(active)
	holds, err := tx.holds()
	if err != nil {
		return out, err
	}
	for _, h := range holds {
		if h.PolicyID == p.ID && h.Revision == p.Revision && (p.Spec.ResumeRule == "manual" || h.PeriodStart.Equal(month)) {
			out.Held = true
		}
	}
	return out, nil
}

func computeBillingSeconds(leases []financial.ComputeLease, start time.Time, seconds int64, price financial.Price, covered bool) int64 {
	if len(leases) == 0 {
		if covered {
			return 0
		}
		return seconds
	}
	end := start.Add(time.Minute)
	funded := int64(0)
	strict := false
	for _, l := range leases {
		if !l.Strict {
			if l.StartedAt.Before(end) && (l.SettledAt == nil || l.SettledAt.After(start)) {
				return seconds
			}
			continue
		}
		strict = true
		if !computeRateFits(price, l) {
			continue
		}
		from, until := start, l.FundedUntil
		if l.StartedAt.After(from) {
			from = l.StartedAt
		}
		if l.SettledAt != nil && l.SettledAt.Before(until) {
			until = *l.SettledAt
		}
		if until.After(end) {
			until = end
		}
		if from.Before(until) {
			funded += int64(until.Sub(from) / time.Second)
		}
	}
	if !strict {
		if covered {
			return 0
		}
		return seconds
	}
	return min(seconds, funded)
}

func computeRateFits(p financial.Price, l financial.ComputeLease) bool {
	if p.Version == "" {
		p.MillicentsPerUnit = api.OverageMillicentsPerGBHour
		p.UnitQuantity = api.SecondsPerGBHour
	}
	if l.RateQuantity <= 0 || p.UnitQuantity <= 0 || p.MillicentsPerUnit < 0 || l.RateMillicents < 0 {
		return false
	}
	actual := new(big.Int).Mul(big.NewInt(p.MillicentsPerUnit), big.NewInt(l.RateQuantity))
	funded := new(big.Int).Mul(big.NewInt(l.RateMillicents), big.NewInt(p.UnitQuantity))
	return actual.Cmp(funded) <= 0
}

func latestComputeLease(rows []financial.ComputeLease) (financial.ComputeLease, bool) {
	var latest financial.ComputeLease
	for _, l := range rows {
		if latest.ID == "" || l.StartedAt.After(latest.StartedAt) || l.StartedAt.Equal(latest.StartedAt) && l.GrantedAt.After(latest.GrantedAt) {
			latest = l
		}
	}
	return latest, latest.ID != ""
}
