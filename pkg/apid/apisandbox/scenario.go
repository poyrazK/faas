package apisandbox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const SeedEnvironmentKey = "GREGALE_API_SANDBOX_SEED"

func ValidateScenario(input *api.APISandboxScenarioInput) error {
	input.Name = strings.TrimSpace(input.Name)
	if !validGuideText(input.Name, api.APISandboxScenarioMaxName) {
		return fmt.Errorf("enter a scenario name of up to %d characters", api.APISandboxScenarioMaxName)
	}
	seed, err := json.Marshal(input.Seed)
	if err != nil || len(seed) > api.APISandboxSeedMaxBytes || seed[0] != '{' {
		return fmt.Errorf("seed must be a JSON object of up to %d bytes", api.APISandboxSeedMaxBytes)
	}
	input.Seed = seed
	if len(input.SeededIDs) > api.APISandboxScenarioMaxItems {
		return fmt.Errorf("publish up to %d seeded IDs", api.APISandboxScenarioMaxItems)
	}
	for label, id := range input.SeededIDs {
		if !validGuideText(label, api.APISandboxScenarioMaxName) || !validGuideText(id, 256) {
			return fmt.Errorf("seeded ID labels and values must be nonempty text of up to 100 and 256 characters, respectively")
		}
	}
	if len(input.Requests) == 0 || len(input.Requests) > api.APISandboxScenarioMaxItems {
		return fmt.Errorf("include between 1 and %d example requests", api.APISandboxScenarioMaxItems)
	}
	for i := range input.Requests {
		if err := validateExampleRequest(&input.Requests[i]); err != nil {
			return fmt.Errorf("request %d: %w", i+1, err)
		}
	}
	if input.StripeWebhook != nil && input.OutboundWebhook != nil {
		return fmt.Errorf("choose a Stripe webhook or an outbound webhook for this scenario")
	}
	if input.StripeWebhook != nil {
		if err := validateStripeWebhook(input); err != nil {
			return err
		}
	}
	if input.OutboundWebhook != nil {
		job := input.Requests[0]
		if len(input.Requests) != 1 || job.Method != http.MethodPost || job.ExpectedStatus != http.StatusAccepted ||
			len(job.Assertions) != 0 || strings.Contains(job.Path, "?") {
			return fmt.Errorf("an outbound webhook scenario needs one POST job with expected_status 202 and a path without a query. Put result assertions under outbound_webhook")
		}
		if err := validateAssertions(http.MethodPost, input.OutboundWebhook.Assertions); err != nil {
			return fmt.Errorf("outbound_webhook: %w", err)
		}
	}
	definition, err := json.Marshal(input)
	if err != nil || len(definition) > api.APISandboxScenarioMaxBytes {
		return fmt.Errorf("the scenario must fit within %d bytes", api.APISandboxScenarioMaxBytes)
	}
	return nil
}

func validateStripeWebhook(input *api.APISandboxScenarioInput) error {
	webhook := input.StripeWebhook
	if webhook.DeliveryPath == "" || len(webhook.DeliveryPath) > api.InboundWebhookDeliveryPathMaxBytes ||
		!ValidSmokePath(webhook.DeliveryPath) || strings.ContainsAny(webhook.DeliveryPath, "?#") {
		return fmt.Errorf("stripe_webhook.delivery_path must be a sandbox path without a query or fragment")
	}
	if !api.ValidInboundWebhookEventType(webhook.EventType) {
		return fmt.Errorf("stripe_webhook.event_type must be a Stripe event name such as invoice.paid")
	}
	object, err := json.Marshal(webhook.DataObject)
	if err != nil || len(object) > api.APISandboxWebhookMaxBytes || len(object) == 0 || object[0] != '{' {
		return fmt.Errorf("stripe_webhook.data_object must be a JSON object of up to %d bytes", api.APISandboxWebhookMaxBytes)
	}
	webhook.DataObject = object
	if len(input.Requests) != 1 || input.Requests[0].Method != http.MethodGet || len(input.Requests[0].Assertions) == 0 {
		return fmt.Errorf("a Stripe webhook scenario needs one final GET with a JSON assertion")
	}
	return nil
}

func validateExampleRequest(request *api.APISandboxExampleRequest) error {
	request.Title = strings.TrimSpace(request.Title)
	if !validGuideText(request.Title, api.APISandboxScenarioMaxName) {
		return fmt.Errorf("enter a title of up to %d characters", api.APISandboxScenarioMaxName)
	}
	switch request.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("method must be GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	if !validExampleTarget(request.Path) {
		return fmt.Errorf("use a path starting with / on the sandbox host, with no fragment")
	}
	if request.ExpectedStatus < 200 || request.ExpectedStatus > 599 {
		return fmt.Errorf("expected_status must be between 200 and 599")
	}
	if len(request.Body) > 0 {
		if request.Method == "GET" || request.Method == "HEAD" {
			return fmt.Errorf("GET and HEAD requests cannot include a body")
		}
		body, err := json.Marshal(request.Body)
		if err != nil || len(body) > api.APISandboxRequestMaxBytes {
			return fmt.Errorf("body must be valid JSON of up to %d bytes", api.APISandboxRequestMaxBytes)
		}
		request.Body = body
	}
	return validateAssertions(request.Method, request.Assertions)
}

func validExampleTarget(target string) bool {
	path, query, _ := strings.Cut(target, "?")
	if len(target) > 512 || path == "" || !ValidSmokePath(path) ||
		strings.ContainsAny(query, "#\\") || strings.IndexFunc(query, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return false
	}
	_, err := url.ParseQuery(query)
	return err == nil
}

func ValidPartnerLabel(label string) bool {
	return validGuideText(label, api.APISandboxScenarioMaxName)
}

func validGuideText(value string, maxLength int) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= maxLength &&
		!strings.ContainsFunc(value, unicode.IsControl)
}
