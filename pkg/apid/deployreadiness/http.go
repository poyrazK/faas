package deployreadiness

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func Handle(w http.ResponseWriter, r *http.Request, account state.Account, store Reader, config Config) {
	var request api.ProjectDeploymentCheckRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectDeploymentCheckMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		api.WriteProblem(w, api.ErrValidation("require a bounded deployment readiness JSON document"))
		return
	}
	if request.Environment == "" {
		request.Environment = "production"
	}
	if !ValidateRequest(r.PathValue("slug"), request) {
		api.WriteProblem(w, api.ErrValidation("invalid deployment readiness intent"))
		return
	}
	result := Check(r.Context(), store, account, r.PathValue("slug"), request, config)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
