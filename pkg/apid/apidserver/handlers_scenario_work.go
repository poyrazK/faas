package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/rehearsal"
	"net/http"
)

func (s *server) scenarioManagedWorkSupported(w http.ResponseWriter, req api.RegisterScenarioTestRequest) bool {
	if p := rehearsal.ValidateWorkerTermination(req, s.rehearsalWorkerFaultsEnabled); p != nil {
		api.WriteProblem(w, p)
		return false
	}
	if p := rehearsal.ValidateManagedWork(req, s.rehearsalWorkersEnabled); p != nil {
		api.WriteProblem(w, p)
		return false
	}
	return true
}
func scenarioManagedWorkEnrollment(req api.RegisterScenarioTestRequest) (string, string) {
	b, d, _ := api.ScenarioManagedWorkEnrollment(req)
	return b, d
}
func scenarioManagedWorkDigest(req api.RegisterScenarioTestRequest) string {
	_, d := scenarioManagedWorkEnrollment(req)
	return d
}
