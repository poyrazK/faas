// adr: 1750 — amendments 1 and 2: a strict project or environment cap is a
// shared gross book that every attempt in the scope funds alongside its own.
package state

import (
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// computeScopes are the shared books an app attempt belongs to: its current
// project, and the environment its deployment runs in. Jobs have none.
type computeScopes struct {
	Project, Environment, Deployment string
}

// computeSharedScope selects one shared book.
type computeSharedScope struct{ kind, id string }

func (s computeScopes) shared() []computeSharedScope {
	out := []computeSharedScope{}
	if s.Project != "" {
		out = append(out, computeSharedScope{"project", s.Project})
	}
	if s.Environment != "" {
		out = append(out, computeSharedScope{"environment", s.Environment})
	}
	return out
}

func (s computeScopes) id(kind string) string {
	if kind == "project" {
		return s.Project
	}
	return s.Environment
}

// leaseScopes are the shared books an attempt funds.
func leaseScopes(l financial.ComputeLease) computeScopes {
	return computeScopes{Project: l.ProjectID, Environment: l.EnvironmentID, Deployment: l.DeploymentID}
}

func (s computeSharedScope) key() string {
	if s.kind == "project" {
		return financial.ComputeProjectKey(s.id)
	}
	return financial.ComputeEnvironmentKey(s.id)
}

// covers reports whether a strict policy caps this shared book.
func (s computeSharedScope) covers(p financial.BudgetSpec) bool {
	if s.kind == "project" {
		return financial.StrictProjectCompute(p) && p.Scope.ID == s.id
	}
	return financial.StrictEnvironmentCompute(p) && p.Scope.ID == s.id
}

// computeSharedScopeOf parses a shared book's selector.
func computeSharedScopeOf(key string) (computeSharedScope, bool) {
	if id, ok := financial.ComputeProjectID(key); ok {
		return computeSharedScope{"project", id}, true
	}
	if id, ok := financial.ComputeEnvironmentID(key); ok {
		return computeSharedScope{"environment", id}, true
	}
	return computeSharedScope{}, false
}

func setLeaseScope(l *financial.ComputeLease, s computeSharedScope) {
	if s.kind == "project" {
		l.ProjectID = s.id
	} else {
		l.EnvironmentID = s.id
	}
}

// computeScopePolicies returns the enabled strict policies capping one
// shared book.
func computeScopePolicies(tx financialComputeTxn, s computeSharedScope) ([]FinancialBudget, error) {
	if s.id == "" {
		return nil, nil
	}
	rows, err := tx.policies(s.key())
	if err != nil {
		return nil, err
	}
	out := []FinancialBudget{}
	for _, p := range rows {
		if p.DeletedAt == nil && p.Spec.Enabled && s.covers(p.Spec) {
			out = append(out, p)
		}
	}
	return out, nil
}

// computeWorkloadPolicies is the policies FinancialComputePolicies reports for
// a workload: its own and the account's, its current project's, and those of
// every environment of that project. The scheduler, gateways and vmmd then
// treat a shared cap like an app cap; callers that know the deployment narrow
// environment policies with FinancialComputeDeploymentEnvironment.
func computeWorkloadPolicies(tx financialComputeTxn, key string) ([]FinancialBudget, error) {
	all, err := tx.allPolicies()
	if err != nil {
		return nil, err
	}
	// An account without budgets has nothing to scope: every wake of its
	// apps checks this, so the scopes read is skipped.
	if len(all) == 0 {
		return []FinancialBudget{}, nil
	}
	scopes, err := tx.workloadScopes(key, "", "")
	if err != nil {
		return nil, err
	}
	return computeWorkloadPoliciesIn(tx, key, all, scopes)
}

// computeWorkloadPoliciesIn is computeWorkloadPolicies over policies and
// scopes already read.
func computeWorkloadPoliciesIn(tx financialComputeTxn, key string, all []FinancialBudget, scopes computeScopes) ([]FinancialBudget, error) {
	environments, err := computeProjectEnvironmentSet(tx, scopes.Project, all)
	if err != nil {
		return nil, err
	}
	rows := []FinancialBudget{}
	for _, p := range all {
		scope := p.Spec.Scope
		if computePolicyMatches(p, key) || scopes.Project != "" && scope.Kind == "project" && scope.ID == scopes.Project || scope.Kind == "environment" && environments[scope.ID] {
			rows = append(rows, p)
		}
	}
	return rows, nil
}

// computeProjectEnvironmentSet is the project's environments that some policy
// scopes; it reads them only when an environment policy exists.
func computeProjectEnvironmentSet(tx financialComputeTxn, project string, all []FinancialBudget) (map[string]bool, error) {
	if project == "" || !slices.ContainsFunc(all, func(p FinancialBudget) bool { return p.Spec.Scope.Kind == "environment" }) {
		return nil, nil
	}
	ids, err := tx.projectEnvironments(project)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// computeFloorPolicies is every strict policy that waives an app's capacity
// floor: its own, the account's and its current project's. A floor has no
// environment, so environment caps do not waive it.
func computeFloorPolicies(tx financialComputeTxn, key string) ([]FinancialBudget, error) {
	rows, err := tx.policies(key)
	if err != nil {
		return nil, err
	}
	policies := strictComputePolicies(rows, key)
	scopes, err := tx.workloadScopes(key, "", "")
	if err != nil {
		return nil, err
	}
	project, err := computeScopePolicies(tx, computeSharedScope{"project", scopes.Project})
	return append(policies, project...), err
}

// computeAttemptPolicies is every strict policy an attempt must fund: the
// workload's own and the account's, its project's and its deployment's
// environment's. An app in a project whose attempt names no deployment
// fails closed while any environment of the project is capped.
func computeAttemptPolicies(tx financialComputeTxn, key, instance, deployment string) (computeScopes, []FinancialBudget, error) {
	rows, err := tx.policies(key)
	if err != nil {
		return computeScopes{}, nil, err
	}
	policies := strictComputePolicies(rows, key)
	scopes, err := tx.workloadScopes(key, instance, deployment)
	if err != nil {
		return computeScopes{}, nil, err
	}
	for _, s := range scopes.shared() {
		shared, err := computeScopePolicies(tx, s)
		if err != nil {
			return computeScopes{}, nil, err
		}
		policies = append(policies, shared...)
	}
	if scopes.Project != "" && scopes.Deployment == "" {
		all, err := tx.allPolicies()
		if err != nil {
			return computeScopes{}, nil, err
		}
		environments, err := computeProjectEnvironmentSet(tx, scopes.Project, all)
		if err != nil {
			return computeScopes{}, nil, err
		}
		for _, p := range all {
			if p.Spec.Enabled && financial.StrictEnvironmentCompute(p.Spec) && environments[p.Spec.Scope.ID] {
				return computeScopes{}, nil, financial.ErrComputeUnavailable
			}
		}
	}
	return scopes, policies, nil
}

// computeScopeFundingBooks returns the shared books a new strict attempt also
// funds, and records them on the lease. Every unsettled attempt in a scope
// must already fund it: a mixed scope means activation has not finished,
// which fails closed.
func computeScopeFundingBooks(tx financialComputeTxn, l *financial.ComputeLease, scopes computeScopes, policies []FinancialBudget) ([]*financial.ComputeBook, error) {
	out := []*financial.ComputeBook{}
	for _, s := range scopes.shared() {
		if !slices.ContainsFunc(policies, func(p FinancialBudget) bool { return s.covers(p.Spec) }) {
			continue
		}
		b, err := tx.book(s.key(), l.PeriodStart)
		if err != nil {
			return nil, errors.Join(financial.ErrComputeUnavailable, err)
		}
		active, err := tx.active(s.key())
		if err != nil {
			return nil, err
		}
		for _, old := range active {
			if leaseScopes(old).id(s.kind) != s.id || !old.PeriodStart.Equal(l.PeriodStart) {
				return nil, financial.ErrComputeUnavailable
			}
		}
		setLeaseScope(l, s)
		out = append(out, &b)
	}
	return out, nil
}

// computeFundScope moves a shared book by the same gross amounts as the
// workload book: an attempt reserves wanted in place of its old reserve.
func computeFundScope(tx financialComputeTxn, b *financial.ComputeBook, l *financial.ComputeLease, wanted int64, policies []FinancialBudget) error {
	without := b.Reserved - l.Reserved
	if without < 0 {
		return financial.ErrComputeUnavailable
	}
	if wanted > api.FinancialBudgetMoneyMax-b.Committed || without > api.FinancialBudgetMoneyMax-b.Committed-wanted {
		return financial.ErrComputeBudget
	}
	b.Reserved = without + wanted
	return computeCheckPolicies(tx, *b, policies)
}

// computeLeaseScopeBooks reads the shared books an existing attempt funds.
func computeLeaseScopeBooks(tx financialComputeTxn, l financial.ComputeLease) ([]*financial.ComputeBook, error) {
	out := []*financial.ComputeBook{}
	for _, s := range leaseScopes(l).shared() {
		b, err := tx.book(s.key(), l.PeriodStart)
		if err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, nil
}

// computeRenewalScopePolicies checks a renewing attempt's shared books
// against its current scopes. An app that moved into a capped project, or
// a deployment now counted in a capped environment, cannot keep running
// outside that book; a scope the attempt left still bounds it until it
// settles.
func computeRenewalScopePolicies(tx financialComputeTxn, l financial.ComputeLease, scopes computeScopes, policies []FinancialBudget) ([]FinancialBudget, error) {
	funded := leaseScopes(l)
	for _, kind := range []string{"project", "environment"} {
		current, was := scopes.id(kind), funded.id(kind)
		if current == was {
			continue
		}
		now := computeSharedScope{kind, current}
		if current != "" && slices.ContainsFunc(policies, func(p FinancialBudget) bool { return now.covers(p.Spec) }) {
			return nil, financial.ErrComputeUnavailable
		}
		left, err := computeScopePolicies(tx, computeSharedScope{kind, was})
		if err != nil {
			return nil, err
		}
		policies = append(policies, left...)
	}
	return policies, nil
}

// computeBookPolicy reports whether a policy limits a book: the account book
// carries account policies, a shared book its scope's, and a workload book
// its app's or job's.
func computeBookPolicy(b financial.ComputeBook, p financial.BudgetSpec) bool {
	switch {
	case b.ProjectID != "":
		return computeSharedScope{"project", b.ProjectID}.covers(p)
	case b.EnvironmentID != "":
		return computeSharedScope{"environment", b.EnvironmentID}.covers(p)
	case b.JobID != "":
		return financial.StrictJobCompute(p)
	case b.AppID != "":
		return financial.StrictAppCompute(p)
	}
	return financial.StrictAccountCompute(p)
}

// computeEnvironmentCovered reports whether an instance runs in an environment
// capped for the month of at: its residency then bills only within funding,
// and an unfunded VM bills nothing, as for an app cap.
func computeEnvironmentCovered(tx financialComputeTxn, app, instance string, at time.Time) (bool, error) {
	scopes, err := tx.workloadScopes(app, instance, "")
	if err != nil || scopes.Environment == "" {
		return false, err
	}
	s := computeSharedScope{"environment", scopes.Environment}
	month := financial.ComputeMonth(at)
	if _, err := tx.book(s.key(), month); err == nil || !errors.Is(err, ErrNotFound) {
		return err == nil, err
	}
	policies, err := computeScopePolicies(tx, s)
	if err != nil {
		return false, err
	}
	for _, p := range policies {
		if p.UpdatedAt.Before(month.AddDate(0, 1, 0)) {
			return true, nil
		}
	}
	return false, nil
}

// computeFloorWaiving reports whether funding under these policies waives
// the app's capacity floor: every strict cap does except an environment cap.
func computeFloorWaiving(policies []FinancialBudget) bool {
	return slices.ContainsFunc(policies, func(p FinancialBudget) bool { return !financial.StrictEnvironmentCompute(p.Spec) })
}

// computeMarkEnvironmentOnly marks a new app book that only environment caps
// fund, so it does not waive the app's capacity floor.
func computeMarkEnvironmentOnly(tx financialComputeTxn, key string, month time.Time, policies []FinancialBudget) error {
	if computeFloorWaiving(policies) {
		return nil
	}
	b, err := tx.book(key, month)
	if err != nil {
		return err
	}
	b.EnvironmentOnly = true
	return tx.putBook(b)
}

// computeClearEnvironmentOnly lets an activated app cap's book waive the
// app's capacity floor for the month.
func computeClearEnvironmentOnly(tx financialComputeTxn, key string, month time.Time) error {
	b, err := tx.book(key, month)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil || !b.EnvironmentOnly {
		return err
	}
	b.EnvironmentOnly = false
	return tx.putBook(b)
}

// computeEnsureScopes opens the month's shared books an attempt funds.
func computeEnsureScopes(tx financialComputeTxn, scopes computeScopes, policies []FinancialBudget, initialize func(string, bool) error) error {
	for _, s := range scopes.shared() {
		if !slices.ContainsFunc(policies, func(p FinancialBudget) bool { return s.covers(p.Spec) }) {
			continue
		}
		if _, err := tx.book(s.key(), financial.ComputeMonth(tx.clock())); !errors.Is(err, ErrNotFound) {
			if err != nil {
				return err
			}
			continue
		}
		if err := initialize(s.key(), false); err != nil {
			return err
		}
	}
	return nil
}
