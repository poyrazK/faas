package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261111114059 slice 3: a managed Postgres database that never scales
// to zero runs its class's smallest compute all month, so an agent pays for
// it from its budget. A replayed request (its request_id already accepted)
// was charged the first time and is not charged again.

func postgresStanding(spec managedpostgres.Spec) int64 {
	return api.AgentPostgresStandingMillicents(string(spec.Class), spec.ScaleToZero)
}

// chargePostgresCreate prices a new database.
func (s *server) chargePostgresCreate(w http.ResponseWriter, r *http.Request, acct state.Account, requestID string, spec managedpostgres.Spec) (func(), bool) {
	if requestID != "" {
		if _, err := s.managedPostgres.Get(r.Context(), acct.ID, requestID); err == nil {
			return s.noCharge(w, r, acct.Plan)
		}
	}
	return s.chargeAgentBudget(w, r, acct.Plan, 0, postgresStanding(spec))
}

// chargePostgresComputePolicy prices turning scale-to-zero off.
func (s *server) chargePostgresComputePolicy(w http.ResponseWriter, r *http.Request, acct state.Account, database, requestID string, scaleToZero bool) (func(), bool) {
	if _, err := s.managedPostgres.GetComputePolicyChange(r.Context(), acct.ID, database, requestID); err == nil {
		return s.noCharge(w, r, acct.Plan)
	}
	db, err := s.managedPostgres.Get(r.Context(), acct.ID, database)
	if err != nil {
		// The change itself reports the missing database; a preview,
		// which stops here, reports it now.
		return s.postgresChargeTargetMissing(w, r, err)
	}
	after := db.Spec
	after.ScaleToZero = scaleToZero
	return s.chargeAgentBudget(w, r, acct.Plan, postgresStanding(db.Spec), postgresStanding(after))
}

// chargePostgresResize prices a class change of an always-on database.
func (s *server) chargePostgresResize(w http.ResponseWriter, r *http.Request, acct state.Account, database, targetClass string) (func(), bool) {
	if targetClass == "" {
		// A storage-only resize changes no compute.
		return s.noCharge(w, r, acct.Plan)
	}
	db, err := s.managedPostgres.Get(r.Context(), acct.ID, database)
	if err != nil {
		return s.postgresChargeTargetMissing(w, r, err)
	}
	after := db.Spec
	after.Class = managedpostgres.ServiceClass(targetClass)
	return s.chargeAgentBudget(w, r, acct.Plan, postgresStanding(db.Spec), postgresStanding(after))
}

// postgresChargeTargetMissing leaves a missing database for the change to
// report; a preview stops here, so it reports it.
func (s *server) postgresChargeTargetMissing(w http.ResponseWriter, r *http.Request, err error) (func(), bool) {
	if authmw.Previewing(r) {
		managedPostgresProblem(w, err)
		return func() {}, true
	}
	return func() {}, false
}
