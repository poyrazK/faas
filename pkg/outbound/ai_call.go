package outbound

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// aiCallPlan is the gateway's view of one AI call before admission
// (ADR-991): its format, model, output ceiling and reservation.
type aiCallPlan struct {
	endpoint aiEndpoint
	facts    aiCallFacts
	reserve  aiReservation
	guard    AIGuardrails
}

// aiCallRejection is a guardrail refusal sent before admission.
type aiCallRejection struct {
	status int
	code   string
	title  string
}

// planAICall applies an app's model allow-list and output ceiling, adds a
// missing ceiling to the buffered body, and estimates the reservation. It
// returns nil for a call that is not an AI POST.
func planAICall(r *http.Request, integration Integration, bindingAppID, path string, ai *aiRequest) (*aiCallPlan, *aiCallRejection) {
	if integration.AIProvider == "" || r.Method != http.MethodPost {
		return nil, nil
	}
	plan := &aiCallPlan{endpoint: classifyAIEndpoint(integration.AIProvider, path)}
	guarded := false
	if bindingAppID != "" {
		plan.guard, guarded = integration.BindingAIGuardrails[bindingAppID]
	}
	hasBody := r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0
	if ai == nil && hasBody && (providerNeedsBufferedBody(integration.AIProvider) || (guarded && plan.guard.restrictsCalls())) {
		return nil, &aiCallRejection{http.StatusRequestEntityTooLarge, "outbound_ai_request_too_large",
			"AI request body exceeds the size the gateway can check or sign"}
	}
	if ai != nil {
		plan.facts = readAICallFacts(plan.endpoint, ai.body)
	} else {
		plan.facts.model = plan.endpoint.pathModel
	}
	if guarded && !api.OutboundAIModelAllowed(plan.guard.AllowedModels, plan.facts.model) {
		return nil, &aiCallRejection{http.StatusForbidden, "outbound_ai_model_not_allowed", "The model is not allowed for this app"}
	}
	if guarded && plan.guard.MaxOutputTokens != nil && plan.endpoint.generation {
		if rejection := applyOutputCeiling(plan, ai, *plan.guard.MaxOutputTokens); rejection != nil {
			return nil, rejection
		}
	}
	plan.reserve = estimateAIReservation(r, integration, plan, ai, hasBody)
	return plan, nil
}

func applyOutputCeiling(plan *aiCallPlan, ai *aiRequest, ceiling int64) *aiCallRejection {
	unenforceable := &aiCallRejection{http.StatusBadRequest, "outbound_ai_max_tokens_unenforceable",
		"The gateway cannot apply this app's output-token ceiling to the request"}
	if !plan.facts.ceilingKnown || ai == nil {
		return unenforceable
	}
	if plan.facts.outputCeiling > ceiling {
		return &aiCallRejection{http.StatusBadRequest, "outbound_ai_max_tokens_exceeded",
			"The requested output tokens exceed this app's ceiling"}
	}
	if plan.facts.outputCeiling == 0 {
		rewritten, ok := withOutputCeiling(plan.endpoint, ai.body, ceiling)
		if !ok {
			return unenforceable
		}
		ai.body = rewritten
		plan.facts.outputCeiling = ceiling
	}
	return nil
}

// estimateAIReservation charges four body bytes per input token plus the
// output ceiling, or the default reserve, and prices both (ADR-991).
func estimateAIReservation(r *http.Request, integration Integration, plan *aiCallPlan, ai *aiRequest, hasBody bool) aiReservation {
	var inputBytes int64
	switch {
	case ai != nil:
		inputBytes = int64(len(ai.body))
	case hasBody && r.ContentLength > 0:
		inputBytes = r.ContentLength
	case hasBody:
		inputBytes = api.OutboundAIRequestBufferMaxBytes
	}
	input := (inputBytes + api.OutboundAIReserveBytesPerToken - 1) / api.OutboundAIReserveBytesPerToken
	var output int64
	if plan.endpoint.generation {
		output = plan.facts.outputCeiling
		if output == 0 && plan.guard.MaxOutputTokens != nil {
			output = *plan.guard.MaxOutputTokens
		}
		if output == 0 {
			output = api.OutboundAIDefaultOutputReserveTokens
		}
	}
	input = min(input, api.OutboundAIMaxTokensPerResponse)
	output = min(output, api.OutboundAIMaxTokensPerResponse)
	cost := aiCallCostMillicents(integration, AIUsage{Model: plan.facts.model, Known: true, InputTokens: input, OutputTokens: output})
	return aiReservation{tokens: input + output, cost: cost}
}
