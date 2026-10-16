package scenarioconsumer

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	CancelScenarioTestConsumer(context.Context, string, string, string, api.ScenarioTestConsumerRequest) (state.APIConsumer, error)
	EnsureScenarioTestConsumer(context.Context, string, string, string, api.ScenarioTestConsumerRequest) (state.APIConsumer, error)
	LookupScenarioTestConsumer(context.Context, string, string, string, api.ScenarioTestConsumerRequest) (state.APIConsumer, error)
	RevokeScenarioTestConsumer(context.Context, string, string, string, api.ScenarioTestConsumerRequest, string) (state.APIConsumer, error)
}

func Serve(w http.ResponseWriter, r *http.Request, account state.Account, store Store, audits ...func(context.Context, state.APIConsumer)) {
	if account.Plan.ConsumerKeysPerApp() <= 0 {
		api.WriteProblem(w, api.ErrConsumerKeysNotAllowed(account.Plan))
		return
	}
	run, name := r.PathValue("run_id"), r.PathValue("name")
	req, err := parseRequest(r)
	intent, invalid := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || invalid != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid consumer intent", "valid run, consumer name, app ID and expiry are required"))
		return
	}
	consumer, err := applyIntent(r, account.ID, store, run, name, req)
	out := api.ScenarioTestConsumerReceipt{SchemaVersion: 2, IntentSHA256: intent, ExpiresAt: req.ExpiresAt.UTC().Truncate(time.Microsecond)}
	if errors.Is(err, state.ErrScenarioConsumerCancelled) || (err == nil && r.Method == http.MethodDelete && r.URL.Query().Get("consumer_id") == "") {
		out.Cancelled = true
		if errors.Is(err, state.ErrScenarioConsumerCancelled) {
			err = nil
		}
	}
	if errors.Is(err, state.ErrNotFound) && r.Method == http.MethodGet {
		httpkit.WriteJSON(w, 200, out)
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if consumer.ID != "" {
		out.Consumer = &api.APIConsumerResponse{ID: consumer.ID, AppID: consumer.AppID, ExternalRef: consumer.ExternalRef, Name: consumer.Name, Status: string(consumer.Status), CreatedAt: consumer.CreatedAt, UpdatedAt: consumer.UpdatedAt, RevokedAt: consumer.RevokedAt, ManagedByPlatformTenant: consumer.PlatformTenantManaged}
	}
	if consumer.ID != "" && r.Method != http.MethodGet && len(audits) > 0 && audits[0] != nil {
		audits[0](r.Context(), consumer)
	}
	httpkit.WriteJSON(w, 200, out)
}

func applyIntent(r *http.Request, account string, store Store, run, name string, req api.ScenarioTestConsumerRequest) (state.APIConsumer, error) {
	switch r.Method {
	case http.MethodPut:
		return store.EnsureScenarioTestConsumer(r.Context(), account, run, name, req)
	case http.MethodDelete:
		if id := r.URL.Query().Get("consumer_id"); id != "" {
			return store.RevokeScenarioTestConsumer(r.Context(), account, run, name, req, id)
		}
		return store.CancelScenarioTestConsumer(r.Context(), account, run, name, req)
	default:
		return store.LookupScenarioTestConsumer(r.Context(), account, run, name, req)
	}
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, api.CodeValidation, "Consumer intent conflict", "creation identity or input changed, or creation was cancelled"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Test session unavailable", "the owned app or a live registered test session is unavailable"))
	default:
		api.WriteProblem(w, api.ErrCapacity("test consumer intent"))
	}
}

func parseRequest(r *http.Request) (api.ScenarioTestConsumerRequest, error) {
	var req api.ScenarioTestConsumerRequest
	if r.Method == http.MethodPut {
		err := httpkit.DecodeJSON(r, &req)
		return req, err
	}
	req.AppID = r.URL.Query().Get("app_id")
	var err error
	req.ExpiresAt, err = time.Parse(time.RFC3339Nano, r.URL.Query().Get("expires_at"))
	return req, err
}
