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
	// content is what the content policy found and redacted (ADR-997).
	content *contentScan
	// path is the escaped provider path, kept with captured content so a
	// replay can resend the call (ADR-997).
	path string
	// unmetered marks a write the gateway does not meter that the AI policy
	// lets through outside the caps (ADR-1432).
	unmetered bool
}

// aiCallRejection is a guardrail refusal sent before admission.
type aiCallRejection struct {
	status int
	code   string
	title  string
}

// unmeteredRejection refuses a write the gateway cannot meter (ADR-1432).
var unmeteredRejection = &aiCallRejection{http.StatusForbidden, "outbound_ai_endpoint_unmetered",
	"The gateway cannot meter this AI call against the integration's caps; set allow_unmetered in the AI policy to let such calls through"}

// planAICall applies an app's model allow-list and output ceiling, adds a
// missing ceiling to the buffered body, and estimates the reservation. It
// returns nil for a call that is not an AI write. A write the gateway
// neither meters nor knows to be free is refused unless the AI policy
// allows unmetered calls (ADR-1432).
func planAICall(r *http.Request, integration Integration, bindingAppID, path string, ai *aiRequest,
	form map[string]string) (*aiCallPlan, *aiCallRejection) {
	if integration.AIProvider == "" {
		return nil, nil
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodPut, http.MethodPatch:
		if !integration.AIAllowUnmetered {
			return nil, unmeteredRejection
		}
		return &aiCallPlan{path: path, unmetered: true}, nil
	default:
		return nil, nil
	}
	plan := &aiCallPlan{endpoint: classifyAIEndpoint(integration.AIProvider, path), path: path}
	if !plan.endpoint.metered() && !aiWriteFree(integration.AIProvider, path) {
		if !integration.AIAllowUnmetered {
			return nil, unmeteredRejection
		}
		plan.unmetered = true
	}
	guarded := false
	if bindingAppID != "" {
		plan.guard, guarded = integration.BindingAIGuardrails[bindingAppID]
	}
	hasBody := r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0
	content, rejection := applyContentPolicy(r, integration, ai, hasBody)
	if rejection != nil {
		return &aiCallPlan{content: content}, rejection
	}
	plan.content = content
	if ai == nil && hasBody && (providerNeedsBufferedBody(integration.AIProvider) || (guarded && plan.guard.restrictsCalls())) {
		return nil, &aiCallRejection{http.StatusRequestEntityTooLarge, "outbound_ai_request_too_large",
			"AI request body exceeds the size the gateway can check or sign"}
	}
	if ai != nil {
		plan.facts = readAICallFacts(plan.endpoint, ai.body)
	} else {
		plan.facts.model = plan.endpoint.pathModel
		if plan.endpoint.media == aiMediaImage {
			plan.facts.images = imageCount(plan.endpoint, 0)
		}
	}
	applyFormFacts(plan.endpoint, &plan.facts, form)
	if plan.endpoint.media == aiMediaVideo && !videoMeterable(integration, plan.facts) {
		// A video the gateway cannot size or price is unmetered.
		if !integration.AIAllowUnmetered {
			return nil, unmeteredRejection
		}
		plan.unmetered, plan.endpoint.media = true, aiMediaNone
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
// output ceiling, or the default reserve, and prices both (ADR-991). Media
// calls reserve in the unit their price bills (ADR-997).
func estimateAIReservation(r *http.Request, integration Integration, plan *aiCallPlan, ai *aiRequest, hasBody bool) aiReservation {
	var inputBytes int64
	switch {
	case ai != nil:
		inputBytes = int64(len(ai.body))
	case hasBody && r.ContentLength > 0:
		inputBytes = r.ContentLength
	case hasBody && plan.endpoint.media == aiMediaTranscription:
		inputBytes = api.OutboundAIAudioReserveMaxBytes
	case hasBody:
		inputBytes = api.OutboundAIRequestBufferMaxBytes
	}
	estimate := AIUsage{Model: plan.facts.model, Known: true,
		InputTokens: (inputBytes + api.OutboundAIReserveBytesPerToken - 1) / api.OutboundAIReserveBytesPerToken}
	if plan.endpoint.media != aiMediaNone {
		estimateMediaReservation(&estimate, integration, plan, inputBytes)
	} else if plan.endpoint.generation {
		estimate.OutputTokens = plan.facts.outputCeiling
		if estimate.OutputTokens == 0 && plan.guard.MaxOutputTokens != nil {
			estimate.OutputTokens = *plan.guard.MaxOutputTokens
		}
		if estimate.OutputTokens == 0 {
			estimate.OutputTokens = api.OutboundAIDefaultOutputReserveTokens
		}
	}
	// ADR-1433: the request's built-in tools reserve their fees.
	estimate.WebSearches, estimate.FileSearches = plan.facts.tools.webSearches, plan.facts.tools.fileSearches
	estimate.CodeSessionGB = plan.facts.tools.codeSessionGB
	estimate.InputTokens = min(estimate.InputTokens, api.OutboundAIMaxTokensPerResponse)
	estimate.OutputTokens = min(estimate.OutputTokens, api.OutboundAIMaxTokensPerResponse)
	estimate.OutputImageTokens = min(estimate.OutputImageTokens, estimate.OutputTokens)
	cost := aiCallCostMillicents(integration, estimate)
	return aiReservation{tokens: estimate.InputTokens + estimate.OutputTokens, cost: cost, estimate: estimate}
}

// estimateMediaReservation reserves an image, transcription or speech call
// (ADR-997). A model priced per image, per audio minute or per character
// reserves that unit; any other model reserves tokens: output image tokens
// per image, audio tokens per estimated second, or the default output
// reserve for speech.
func estimateMediaReservation(estimate *AIUsage, integration Integration, plan *aiCallPlan, inputBytes int64) {
	price, priced := aiPriceFor(integration, plan.facts.model)
	switch plan.endpoint.media {
	case aiMediaImage:
		// An edit uploads images whose bytes say little about their
		// tokens; the prompt and inputs reserve at most two images' worth.
		estimate.InputTokens = min(estimate.InputTokens, 2*api.OutboundAIImageReserveOutputTokens)
		estimate.Images = plan.facts.images
		if !priced || price.Image == 0 {
			estimate.OutputTokens = plan.facts.images * api.OutboundAIImageReserveOutputTokens
			estimate.OutputImageTokens = estimate.OutputTokens
		}
	case aiMediaTranscription:
		seconds := (inputBytes + api.OutboundAIAudioReserveBytesPerSecond - 1) / api.OutboundAIAudioReserveBytesPerSecond
		estimate.InputTokens = 0
		if priced && price.AudioMinute > 0 {
			estimate.AudioSeconds = seconds
		} else {
			estimate.InputTokens = seconds * api.OutboundAIAudioReserveTokensPerSecond
		}
	case aiMediaSpeech:
		estimate.Characters = plan.facts.characters
		if priced && price.Characters > 0 {
			estimate.InputTokens = 0
		} else {
			estimate.OutputTokens = api.OutboundAIDefaultOutputReserveTokens
		}
	case aiMediaVideo:
		// ADR-1432: a video bills its generated seconds, not tokens.
		estimate.InputTokens = 0
		estimate.VideoSeconds, estimate.VideoResolution = plan.facts.videoSeconds, plan.facts.videoResolution
	}
}

// videoMeterable reports a video call the gateway can size and price: its
// body says its seconds, and its model has a price per second at its
// resolution (ADR-1432).
func videoMeterable(integration Integration, facts aiCallFacts) bool {
	if !facts.videoSized || facts.model == "" {
		return false
	}
	usage := AIUsage{Model: facts.model, VideoSeconds: facts.videoSeconds, VideoResolution: facts.videoResolution}
	price, ok := aiPriceFor(integration, usage.priceModel())
	return ok && price.VideoSecond > 0
}
