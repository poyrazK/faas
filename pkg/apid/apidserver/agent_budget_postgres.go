package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
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
			return func() {}, false
		}
	}
	return s.chargeAgentBudget(w, r, acct.Plan, 0, postgresStanding(spec))
}

// chargePostgresComputePolicy prices turning scale-to-zero off.
func (s *server) chargePostgresComputePolicy(w http.ResponseWriter, r *http.Request, acct state.Account, database, requestID string, scaleToZero bool) (func(), bool) {
	if _, err := s.managedPostgres.GetComputePolicyChange(r.Context(), acct.ID, database, requestID); err == nil {
		return func() {}, false
	}
	db, err := s.managedPostgres.Get(r.Context(), acct.ID, database)
	if err != nil {
		// The change itself reports the missing database.
		return func() {}, false
	}
	after := db.Spec
	after.ScaleToZero = scaleToZero
	return s.chargeAgentBudget(w, r, acct.Plan, postgresStanding(db.Spec), postgresStanding(after))
}

// chargePostgresResize prices a class change of an always-on database.
func (s *server) chargePostgresResize(w http.ResponseWriter, r *http.Request, acct state.Account, database, targetClass string) (func(), bool) {
	db, err := s.managedPostgres.Get(r.Context(), acct.ID, database)
	if err != nil || targetClass == "" {
		return func() {}, false
	}
	after := db.Spec
	after.Class = managedpostgres.ServiceClass(targetClass)
	return s.chargeAgentBudget(w, r, acct.Plan, postgresStanding(db.Spec), postgresStanding(after))
}
