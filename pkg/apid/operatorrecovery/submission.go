// Package operatorrecovery implements durable HTTP recovery acceptance (ADR-1524).
package operatorrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type Notifier interface {
	Notify(context.Context, string, string) error
}

type Auditor interface {
	Emit(context.Context, string, *string, map[string]any)
}

type Service struct {
	Store    state.OperatorRecoverySubmissionStore
	Notifier Notifier
	Log      *slog.Logger
	Audit    Auditor // Rejections only; accepted request audits use the atomic outbox.
}

func (s Service) Submit(w http.ResponseWriter, r *http.Request, actor state.Account, kind state.OperatorIntentKind) {
	in, problem := request(r, actor.ID, kind)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if s.Store == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "operator_submission_unavailable", "recovery submissions unavailable", "durable recovery submission storage is required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.OperatorRecoverySubmissionTimeout)
	defer cancel()
	receipt, err := s.Store.SubmitOperatorRecovery(ctx, in)
	if err != nil {
		s.writeError(w, r, in, err)
		return
	}
	if !receipt.Replayed && s.Notifier != nil {
		payload, _ := json.Marshal(map[string]string{"intent_id": receipt.IntentID, "kind": string(receipt.Kind), "target_id": receipt.TargetID})
		if err := s.Notifier.Notify(ctx, db.NotifyOperatorIntent, string(payload)); err != nil && s.Log != nil {
			s.Log.Warn("apid: recovery notification failed", "intent_id", receipt.IntentID, "err", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(receipt.ResponseBody)
}

func request(r *http.Request, actor string, kind state.OperatorIntentKind) (state.OperatorRecoverySubmission, *api.Problem) {
	in := state.OperatorRecoverySubmission{ActorID: actor, Kind: kind, RequestKey: r.Header.Get("Idempotency-Key"), TraceID: middleware.TraceIDFrom(r)}
	invalid := func(detail string) (state.OperatorRecoverySubmission, *api.Problem) {
		p := api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid recovery request", detail)
		return in, p
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 || !state.ValidOperatorRecoveryKey(in.RequestKey) {
		return invalid("one Idempotency-Key of 1 to 255 visible ASCII characters is required")
	}
	// ParseQuery reports malformed escapes instead of silently dropping a parameter.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["confirm"]) != 1 || query.Get("confirm") != "true" {
		return invalid("one ?confirm=true is required")
	}
	for key, values := range query {
		if key != "confirm" && key != "reason" || len(values) != 1 {
			return invalid("only confirm and reason query parameters are accepted, each once")
		}
	}
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(body) != 0 {
			return invalid("recovery requests require an empty body")
		}
	}
	in.Target, in.Reason = r.PathValue("id"), query.Get("reason")
	if kind == state.OperatorIntentKindForceColdBoot {
		in.Target = r.PathValue("slug")
	} else if id, err := uuid.Parse(in.Target); err != nil || id == uuid.Nil {
		p := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "instance not found", "instance id is not a valid nonzero uuid")
		return in, p
	}
	in, err = state.NormalizeOperatorRecovery(in)
	if err != nil {
		return invalid("reason must match [a-z0-9_]{1,64} and target must be valid")
	}
	return in, nil
}

func (s Service) writeError(w http.ResponseWriter, r *http.Request, in state.OperatorRecoverySubmission, err error) {
	var ineligible *state.OperatorRecoveryIneligibleError
	switch {
	case errors.Is(err, state.ErrOperatorRecoveryKeyConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operator_submission_conflict", "request key conflict", "reuse the key with the original action, target and reason"))
	case errors.Is(err, state.ErrOperatorRecoveryLegacyKey):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operator_submission_legacy", "legacy submission requires reconciliation", "inspect the existing operator intent before choosing a new request key"))
	case errors.As(err, &ineligible):
		s.rejectAudit(r, in, ineligible)
		code, title := "instance_not_parkable", "instance is not in a parkable state"
		if in.Kind == state.OperatorIntentKindForceRestart {
			code, title = "instance_not_restartable", "instance is not in a restartable state"
		}
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, code, title, err.Error()))
	case errors.Is(err, state.ErrNotFound):
		title := "instance not found"
		if in.Kind == state.OperatorIntentKindForceColdBoot {
			title = "app not found"
		}
		if errors.Is(err, state.ErrOperatorRecoveryDeployment) {
			title = "deployment not found"
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, title, "recovery target or its owner could not be resolved"))
	default:
		if s.Log != nil {
			s.Log.Error("apid: recovery submission failed", "kind", string(in.Kind), "err", err)
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "recovery submission failed", "retry the identical request with the same Idempotency-Key"))
	}
}

func (s Service) rejectAudit(r *http.Request, in state.OperatorRecoverySubmission, target *state.OperatorRecoveryIneligibleError) {
	if s.Audit == nil {
		return
	}
	s.Audit.Emit(r.Context(), state.OperatorRecoveryAuditKind(in.Kind), &target.AccountID, map[string]any{
		"actor": in.ActorID, "target_account_id": target.AccountID, "instance_id": target.InstanceID,
		"app_id": target.AppID, "deployment_id": target.DeploymentID, "previous_state": target.State,
		"reason": in.Reason, "result": "rejected", "intent_id": "",
	})
}
