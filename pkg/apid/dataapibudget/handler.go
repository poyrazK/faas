// Package dataapibudget admits optional fleet-wide owner request budgets.
package dataapibudget

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapiworkload"
	budget "github.com/onebox-faas/faas/pkg/state/dataapibudget"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type Store interface {
	Take(context.Context, string, string, string, int, int) (budget.Decision, error)
}
type Handler struct {
	Store     Store
	Verifier  *workloadidentity.Verifier
	Workloads dataapiworkload.Store
}

var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, app, err := dataapiworkload.Live(r, h.Verifier, workloadidentity.DataAPIBudgetAudience, h.Workloads, time.Now())
	if err != nil {
		dataapiworkload.Refuse(w, err, "a Data API budget workload assertion from a live production instance is required")
		return
	}
	var body struct {
		Version  int    `json:"version"`
		Key      string `json:"key"`
		Requests int    `json:"requests"`
		Window   int    `json:"window_seconds"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.DataAPIHealthMaxBytes))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || !errors.Is(d.Decode(new(any)), io.EOF) || body.Version != 1 || !digest.MatchString(body.Key) || body.Requests < 1 || body.Requests > api.DataAPISharedBudgetMaxRequests || body.Window < 1 || body.Window > api.DataAPISharedBudgetMaxWindowSeconds {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid shared budget", "use a version 1 bounded budget request"))
		return
	}
	if h.Store == nil {
		unavailable(w)
		return
	}
	decision, err := h.Store.Take(r.Context(), app.AccountID, app.ID, body.Key, body.Requests, body.Window)
	if err != nil {
		unavailable(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(decision)
}
func unavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(503, "data_api_shared_budget_unavailable", "Shared request budget unavailable", "request admission could not be confirmed; no customer SQL was dispatched"))
}
