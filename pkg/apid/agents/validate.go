package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

var (
	agentName = regexp.MustCompile(fmt.Sprintf(`^[a-z][a-z0-9-]{0,%d}$`, api.AgentNameMaxLen-1))
	envName   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	dedupeKey = regexp.MustCompile(fmt.Sprintf(`^[\x21-\x7e]{1,%d}$`, api.AgentRunDedupeKeyMaxLen))
	// repository and commit match agent_runs' CHECKs: owner/name, and a
	// full commit SHA so a run always sees the same tree.
	repository = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
	commitSHA  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// resultField names a top-level result.json string a delivery carries.
	resultField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	// objectPrefix is a bucket delivery's key prefix: S3's safe characters,
	// ending in a slash.
	objectPrefix = regexp.MustCompile(`^[A-Za-z0-9!_.*'()-][A-Za-z0-9!_.*'()/-]{0,199}/$`)
)

// deliveryTypes are the destinations a version may declare (ADR-20261027090010 §8).
var deliveryTypes = map[string]bool{api.AgentDeliveryGitHubComment: true, api.AgentDeliveryBucket: true, api.AgentDeliveryEmail: true}

var modelProviders = map[string]bool{api.AgentModelOpenAI: true, api.AgentModelAnthropic: true}

var runStates = map[string]bool{
	agentstore.StateQueued: true, agentstore.StateStarting: true, agentstore.StateRunning: true,
	agentstore.StateDelivering: true, agentstore.StateSucceeded: true, agentstore.StateFailed: true,
	agentstore.StateTimedOut: true, agentstore.StateOutOfMemory: true, agentstore.StateCancelled: true,
}

func validName(name string) *api.Problem {
	if !agentName.MatchString(name) {
		return api.ErrValidation(fmt.Sprintf("name must be 1-%d characters of a-z, 0-9 and '-', starting with a letter", api.AgentNameMaxLen))
	}
	return nil
}

func validDescription(d string) *api.Problem {
	if utf8.RuneCountInString(d) > api.AgentDescriptionMaxLen || !utf8.ValidString(d) || strings.ContainsRune(d, 0) {
		return api.ErrValidation(fmt.Sprintf("description must be valid text of at most %d characters", api.AgentDescriptionMaxLen))
	}
	return nil
}

// versionSpec validates a version against the account's plan.
func versionSpec(in api.AgentVersionSpec, plan api.Plan) (agentstore.VersionSpec, *api.Problem) {
	harness, prob := validHarness(in.Harness)
	if prob != nil {
		return agentstore.VersionSpec{}, prob
	}
	if prob := validText("prompt_template", in.PromptTemplate, api.AgentPromptTemplateMaxBytes); prob != nil {
		return agentstore.VersionSpec{}, prob
	}
	if in.PromptTemplate == "" {
		return agentstore.VersionSpec{}, api.ErrValidation("prompt_template is required")
	}
	idx := plan.PlanIndex()
	ramMax, timeoutMax := api.AgentRunRAMMBMax[idx], api.AgentRunTimeoutMaxSec[idx]
	switch {
	case in.RAMMB < 0 || (in.RAMMB > 0 && (in.RAMMB < api.AgentRunRAMMBMin || in.RAMMB%api.AgentRunRAMMBMin != 0)):
		return agentstore.VersionSpec{}, api.ErrValidation(fmt.Sprintf("ram_mb must be a multiple of %d MB, or omitted to size runs automatically", api.AgentRunRAMMBMin))
	case in.RAMMB > ramMax:
		return agentstore.VersionSpec{}, api.ErrAgentLimit(plan, "AgentRunRAMMBMax", ramMax, in.RAMMB)
	case in.TimeoutSec < 0:
		return agentstore.VersionSpec{}, api.ErrValidation("timeout_sec must be positive, or omitted for the default")
	}
	timeout := in.TimeoutSec
	if timeout == 0 {
		timeout = min(api.AgentRunTimeoutDefaultSec, timeoutMax)
	}
	if timeout == 0 || timeout > timeoutMax {
		return agentstore.VersionSpec{}, api.ErrAgentLimit(plan, "AgentRunTimeoutMaxSec", timeoutMax, max(timeout, 1))
	}
	integrations, prob := validIntegrations(in.Integrations, harness.Models)
	if prob != nil {
		return agentstore.VersionSpec{}, prob
	}
	if in.AIBudgetMillicents < 0 || in.AIBudgetMillicents > api.MaxOutboundAICostMillicents {
		return agentstore.VersionSpec{}, api.ErrValidation(fmt.Sprintf(
			"ai_budget_millicents must be between 1 and %d, or omitted for the plan's default", api.MaxOutboundAICostMillicents))
	}
	deliveries, prob := validDeliveries(in.Deliveries)
	if prob != nil {
		return agentstore.VersionSpec{}, prob
	}
	return agentstore.VersionSpec{Harness: harness, PromptTemplate: in.PromptTemplate, RAMMB: in.RAMMB, TimeoutSec: timeout,
		Integrations: integrations, AIBudgetMillicents: in.AIBudgetMillicents, Deliveries: deliveries}, nil
}

// validDeliveries allows each known destination once. A comment or an email
// carries a named result.json string; an email goes only to the account's
// own address; a bucket delivery names one of the
// account's buckets and a key prefix, and the store checks the bucket.
func validDeliveries(in []api.AgentDelivery) ([]agentstore.Delivery, *api.Problem) {
	if len(in) > api.AgentDeliveriesPerVersionMax {
		return nil, api.ErrValidation(fmt.Sprintf("a version declares at most %d deliveries", api.AgentDeliveriesPerVersionMax))
	}
	var out []agentstore.Delivery
	seen := map[string]bool{}
	for i, d := range in {
		if prob := validDelivery(i, d, seen[d.Type]); prob != nil {
			return nil, prob
		}
		seen[d.Type] = true
		delivery := agentstore.Delivery{Type: d.Type, Field: d.Field}
		if d.Type == api.AgentDeliveryBucket {
			delivery.Field, delivery.Bucket, delivery.Prefix = "", strings.ToLower(d.Bucket), d.Prefix
			if delivery.Prefix == "" {
				delivery.Prefix = api.AgentDeliveryDefaultPrefix
			}
		}
		out = append(out, delivery)
	}
	return out, nil
}

func validDelivery(i int, d api.AgentDelivery, seen bool) *api.Problem {
	_, bucketErr := uuid.Parse(d.Bucket)
	bucket := d.Type == api.AgentDeliveryBucket
	switch {
	case !deliveryTypes[d.Type]:
		return api.ErrValidation(fmt.Sprintf("deliveries[%d].type must be %q, %q or %q", i,
			api.AgentDeliveryGitHubComment, api.AgentDeliveryBucket, api.AgentDeliveryEmail))
	case seen:
		return api.ErrValidation(fmt.Sprintf("deliveries[%d]: a version declares each delivery type once", i))
	case d.Field != "" && (bucket || !resultField.MatchString(d.Field)):
		return api.ErrValidation(fmt.Sprintf("deliveries[%d].field must name a top-level result.json string; a bucket delivery has none", i))
	case bucket && bucketErr != nil:
		return api.ErrValidation(fmt.Sprintf("deliveries[%d].bucket must be a bucket ID", i))
	case bucket && d.Prefix != "" && (!objectPrefix.MatchString(d.Prefix) || strings.Contains(d.Prefix, "//") ||
		strings.Contains("/"+d.Prefix, "/../") || strings.Contains("/"+d.Prefix, "/./")):
		return api.ErrValidation(fmt.Sprintf("deliveries[%d].prefix must be a clean key prefix ending in a slash", i))
	case !bucket && (d.Bucket != "" || d.Prefix != ""):
		return api.ErrValidation(fmt.Sprintf("deliveries[%d]: only a bucket delivery names a bucket or prefix", i))
	}
	return nil
}

// validIntegrations requires each provider to be one the harness reads a
// base URL for, and each value to be an integration ID. The store checks
// the integration itself in the version's transaction.
func validIntegrations(in, models map[string]string) (map[string]string, *api.Problem) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > api.AgentModelIntegrationsMax {
		return nil, api.ErrValidation(fmt.Sprintf("integrations has %d entries; at most %d are allowed", len(in), api.AgentModelIntegrationsMax))
	}
	out := make(map[string]string, len(in))
	for provider, raw := range in {
		if _, ok := models[provider]; !ok {
			return nil, api.ErrValidation(fmt.Sprintf("integrations.%s: the harness reads no base URL for %q (harness.models)", provider, provider))
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, api.ErrValidation(fmt.Sprintf("integrations.%s must be an AI gateway integration ID", provider))
		}
		out[provider] = id.String()
	}
	return out, nil
}

func validHarness(in api.AgentHarness) (agentstore.Harness, *api.Problem) {
	switch {
	case (in.Preset == "") == (in.Image == ""):
		return agentstore.Harness{}, api.ErrValidation("harness needs exactly one of preset and image")
	case in.Preset != "" && !isPreset(in.Preset):
		return agentstore.Harness{}, api.ErrValidation(fmt.Sprintf("harness.preset %q is not one of %s", in.Preset, strings.Join(agentpresets.Names(), ", ")))
	case in.Image != "" && len(in.Run) == 0:
		return agentstore.Harness{}, api.ErrValidation("harness.run is required for an image")
	case len(in.Run) > api.AgentHarnessArgsMax:
		return agentstore.Harness{}, api.ErrValidation(fmt.Sprintf("harness.run has %d entries; at most %d are allowed", len(in.Run), api.AgentHarnessArgsMax))
	}
	if in.Image != "" {
		_, err := oci.ParseReference(in.Image)
		if err != nil || len(in.Image) > api.AgentImageRefMaxBytes || strings.ContainsFunc(in.Image, unicode.IsSpace) {
			return agentstore.Harness{}, api.ErrValidation(fmt.Sprintf("harness.image %q is not a valid image reference", in.Image))
		}
	}
	for i, arg := range in.Run {
		if arg == "" {
			return agentstore.Harness{}, api.ErrValidation(fmt.Sprintf("harness.run[%d] is empty", i))
		}
		if prob := validText(fmt.Sprintf("harness.run[%d]", i), arg, api.AgentHarnessArgMaxBytes); prob != nil {
			return agentstore.Harness{}, prob
		}
	}
	for provider, variable := range in.Models {
		if !modelProviders[provider] {
			return agentstore.Harness{}, api.ErrValidation(fmt.Sprintf("harness.models: %q is not one of openai, anthropic", provider))
		}
		if !envName.MatchString(variable) || strings.HasPrefix(variable, "FAAS_") || strings.HasPrefix(variable, "GREGALE_") {
			return agentstore.Harness{}, api.ErrValidation(fmt.Sprintf("harness.models.%s must name an environment variable outside FAAS_ and GREGALE_", provider))
		}
	}
	return withPresetDefaults(agentstore.Harness{Preset: in.Preset, Image: in.Image, Run: in.Run, Models: in.Models}), nil
}

func isPreset(name string) bool {
	_, ok := agentpresets.Lookup(name)
	return ok
}

// withPresetDefaults records a preset's command and model variables on the
// version when the caller gave none, so a version shows exactly what runs
// and stays fixed when the catalog's defaults change later.
func withPresetDefaults(h agentstore.Harness) agentstore.Harness {
	preset, ok := agentpresets.Lookup(h.Preset)
	if !ok {
		return h
	}
	if len(h.Run) == 0 {
		h.Run = preset.Run
	}
	if len(h.Models) == 0 {
		h.Models = preset.Models
	}
	return h
}

// runRequest validates a run admission's caller-supplied fields.
func runRequest(in api.CreateAgentRunRequest) (agentstore.Admission, *api.Problem) {
	a := agentstore.Admission{Version: in.Version, DedupeKey: in.DedupeKey, Prompt: in.Prompt, Trigger: "api"}
	if in.Version < 0 {
		return a, api.ErrValidation("version must be positive, or omitted for the latest")
	}
	if in.Tenant != "" {
		id, err := uuid.Parse(in.Tenant)
		if err != nil {
			return a, api.ErrValidation("tenant must be a platform tenant ID")
		}
		a.TenantID = id.String()
	}
	if in.DedupeKey != "" && !dedupeKey.MatchString(in.DedupeKey) {
		return a, api.ErrValidation(fmt.Sprintf("dedupe_key must be 1-%d printable ASCII characters without spaces", api.AgentRunDedupeKeyMaxLen))
	}
	if in.Prompt != nil {
		if prob := validText("prompt", *in.Prompt, api.AgentRunPromptMaxBytes); prob != nil {
			return a, prob
		}
	}
	if in.Checkout != nil {
		checkout, prob := validCheckout(*in.Checkout)
		if prob != nil {
			return a, prob
		}
		a.Checkout = &checkout
	}
	if in.GitHub != nil {
		target, prob := validGitHubTarget(*in.GitHub)
		if prob != nil {
			return a, prob
		}
		a.GitHub = &target
	}
	inputs := bytes.TrimSpace(in.Inputs)
	if len(inputs) == 0 || bytes.Equal(inputs, []byte("null")) {
		return a, nil
	}
	var object map[string]json.RawMessage
	if len(inputs) > api.AgentRunInputsMaxBytes || json.Unmarshal(inputs, &object) != nil || object == nil {
		return a, api.ErrValidation(fmt.Sprintf("inputs must be a JSON object of at most %d bytes", api.AgentRunInputsMaxBytes))
	}
	// Postgres jsonb cannot hold a NUL character.
	if bytes.Contains(inputs, []byte(`\u0000`)) {
		return a, api.ErrValidation("inputs must not contain NUL characters")
	}
	a.Inputs = json.RawMessage(inputs)
	return a, nil
}

func validCheckout(in api.AgentCheckout) (agentstore.Checkout, *api.Problem) {
	commit := strings.ToLower(in.Commit)
	owner, name, _ := strings.Cut(in.Repository, "/")
	switch {
	case !repository.MatchString(in.Repository) || owner == "." || owner == ".." || name == "." || name == "..":
		return agentstore.Checkout{}, api.ErrValidation("checkout.repository must be a GitHub repository as owner/name")
	case !commitSHA.MatchString(commit):
		return agentstore.Checkout{}, api.ErrValidation("checkout.commit must be a full 40-character commit SHA")
	case in.InstallationID < 0:
		return agentstore.Checkout{}, api.ErrValidation("checkout.installation_id must be a GitHub installation ID, or omitted")
	}
	return agentstore.Checkout{Repository: in.Repository, Commit: commit, InstallationID: in.InstallationID}, nil
}

func validGitHubTarget(in api.AgentGitHubTarget) (agentstore.GitHubTarget, *api.Problem) {
	owner, name, _ := strings.Cut(in.Repository, "/")
	switch {
	case !repository.MatchString(in.Repository) || owner == "." || owner == ".." || name == "." || name == "..":
		return agentstore.GitHubTarget{}, api.ErrValidation("github.repository must be a GitHub repository as owner/name")
	case in.Number <= 0:
		return agentstore.GitHubTarget{}, api.ErrValidation("github.number must be an issue or pull request number")
	case in.InstallationID < 0:
		return agentstore.GitHubTarget{}, api.ErrValidation("github.installation_id must be a GitHub installation ID, or omitted")
	}
	return agentstore.GitHubTarget{Repository: in.Repository, Number: in.Number, InstallationID: in.InstallationID}, nil
}

func validText(field, s string, maxBytes int) *api.Problem {
	if len(s) > maxBytes {
		return api.ErrValidation(fmt.Sprintf("%s is %d bytes; at most %d are allowed", field, len(s), maxBytes))
	}
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return api.ErrValidation(field + " must be UTF-8 text without NUL characters")
	}
	return nil
}
