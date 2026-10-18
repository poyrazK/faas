package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/repairhealth"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) operatorRepairHealth() repairhealth.Handler {
	store, _ := s.store.(state.DataRepairHealthStore)
	return repairhealth.Handler{Store: store}
}

func (s *server) getOperatorDataRepairHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	s.operatorRepairHealth().ServeHTTP(w, r)
}

func (s *server) adminRepairHealthData(r *http.Request, data dashboard.AdminData) dashboard.AdminData {
	health, problem := s.operatorRepairHealth().Load(r)
	if problem != nil {
		data.RepairHealthError = "Repair health is unavailable. Retry before assuming the queue or retained capacity is empty."
	} else {
		data.RepairHealth = &health
	}
	return data
}
