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
	var req api.ScenarioTestConsumerRequest
	var err error
	if r.Method == http.MethodPut {
		err = httpkit.DecodeJSON(r, &req)
	} else {
		req.AppID = r.URL.Query().Get("app_id")
		req.ExpiresAt, err = time.Parse(time.RFC3339Nano, r.URL.Query().Get("expires_at"))
	}
	intent, invalid := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil || invalid != nil {
		api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid consumer intent", "valid run, consumer name, app ID and expiry are required"))
		return
	}
	var consumer state.APIConsumer
	if r.Method == http.MethodPut {
		consumer, err = store.EnsureScenarioTestConsumer(r.Context(), account.ID, run, name, req)
	} else if r.Method == http.MethodDelete {
		consumer, err = store.RevokeScenarioTestConsumer(r.Context(), account.ID, run, name, req, r.URL.Query().Get("consumer_id"))
	} else {
		consumer, err = store.LookupScenarioTestConsumer(r.Context(), account.ID, run, name, req)
	}
	out := api.ScenarioTestConsumerReceipt{SchemaVersion: 1, IntentSHA256: intent, ExpiresAt: req.ExpiresAt.UTC().Truncate(time.Microsecond)}
	if errors.Is(err, state.ErrNotFound) && r.Method == http.MethodGet {
		httpkit.WriteJSON(w, 200, out)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(409, api.CodeValidation, "Consumer intent conflict", "creation identity or input changed"))
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Test session unavailable", "a live registered test session is required"))
		default:
			api.WriteProblem(w, api.ErrCapacity("test consumer intent"))
		}
		return
	}
	out.Consumer = &api.APIConsumerResponse{ID: consumer.ID, AppID: consumer.AppID, ExternalRef: consumer.ExternalRef, Name: consumer.Name, Status: string(consumer.Status), CreatedAt: consumer.CreatedAt, UpdatedAt: consumer.UpdatedAt, RevokedAt: consumer.RevokedAt, ManagedByPlatformTenant: consumer.PlatformTenantManaged}
	if r.Method != http.MethodGet && len(audits) > 0 && audits[0] != nil {
		audits[0](r.Context(), consumer)
	}
	httpkit.WriteJSON(w, 200, out)
}
