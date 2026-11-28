package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// addAIResult is the --json shape of `gregale add ai` (ADR 20261027132936).
// It never contains the provider key.
type addAIResult struct {
	App                string `json:"app"`
	IntegrationID      string `json:"integration_id"`
	Provider           string `json:"provider"`
	IntegrationCreated bool   `json:"integration_created"`
	KeyStored          bool   `json:"key_stored"`
	// KeyKept reports a key found in the provider's usual variable that was
	// not stored because the integration was reused and no key flag asked.
	KeyKept bool `json:"key_kept,omitempty"`
	// BaseURLKept reports the app's existing GREGALE_AI_BASE_URL left as is.
	BaseURLKept bool              `json:"base_url_kept,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// addAIProvider is how `gregale add ai` sets up one provider: its origin,
// the paths apps may call, and where the key is read by default.
type addAIProvider struct {
	origin   func(region string) (string, error)
	prefixes []string
	keyEnv   string
}

var addAIProviders = map[string]addAIProvider{
	api.OutboundAIProviderOpenAI: {origin: fixedOrigin("https://api.openai.com"), prefixes: []string{"/v1"}, keyEnv: "OPENAI_API_KEY"},
	api.OutboundAIProviderAnthropic: {origin: fixedOrigin("https://api.anthropic.com"), prefixes: []string{"/v1"},
		keyEnv: "ANTHROPIC_API_KEY"},
	api.OutboundAIProviderGemini: {origin: fixedOrigin("https://generativelanguage.googleapis.com"),
		prefixes: []string{"/v1beta", "/v1", "/upload"}, keyEnv: "GEMINI_API_KEY"},
	api.OutboundAIProviderAzureOpenAI: {origin: needsOrigin("https://<resource>.openai.azure.com"), prefixes: []string{"/openai"},
		keyEnv: "AZURE_OPENAI_API_KEY"},
	api.OutboundAIProviderBedrock: {origin: regionalOrigin("https://bedrock-runtime.%s.amazonaws.com"), prefixes: []string{"/model"}},
	api.OutboundAIProviderVertex:  {origin: regionalOrigin("https://%s-aiplatform.googleapis.com"), prefixes: []string{"/v1", "/v1beta1"}},
}

func fixedOrigin(origin string) func(string) (string, error) {
	return func(string) (string, error) { return origin, nil }
}

func needsOrigin(example string) func(string) (string, error) {
	return func(string) (string, error) { return "", fmt.Errorf("pass --origin %s", example) }
}

func regionalOrigin(format string) func(string) (string, error) {
	return func(region string) (string, error) {
		if region == "" || strings.ContainsAny(region, "/.:@ ") {
			return "", errors.New("pass --region, such as us-east-1 or us-central1")
		}
		return fmt.Sprintf(format, region), nil
	}
}

const addAIUsage = "usage: gregale add ai [--app <slug>] --provider <openai|anthropic|gemini|azure_openai|bedrock|vertex> " +
	"[--integration <id>] [--name NAME] [--origin URL] [--region REGION] [--key-env NAME|--key-stdin|--key-file PATH] [--no-env] [--scope SCOPE]"

// cmdAddAI sets up the AI gateway for an app in one step (ADR
// 20261027132936): it creates or reuses an AI integration, stores the
// provider key, binds the integration to the app and points the app's SDK at
// it. The key is read from an environment variable, stdin or a file, never a
// flag value.
func cmdAddAI(args []string) int {
	fs := newFlagSet("add ai", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	provider := fs.String("provider", "", "AI provider")
	integrationID := fs.String("integration", "", "existing AI integration to bind instead of creating one")
	name := fs.String("name", "", "integration name (default the provider)")
	origin := fs.String("origin", "", "provider origin, for Azure OpenAI or an OpenAI-compatible API")
	region := fs.String("region", "", "provider region, for Bedrock and Vertex")
	keyEnv := fs.String("key-env", "", "environment variable holding the provider key")
	keyStdin := fs.Bool("key-stdin", false, "read the provider key from stdin")
	keyFile := fs.String("key-file", "", "file holding the provider key, such as a Vertex service-account key")
	noEnv := fs.Bool("no-env", false, "do not set the app's SDK settings")
	scope := fs.String("scope", "", "environment scope for the app's SDK settings")
	parseErr := parseInterspersed(fs, args)
	// In the app's checkout --app defaults to the linked app, like the
	// other app-scoped commands; add printed usage there (prod hunt r51).
	fillLinkedApp(app)
	if parseErr != nil || fs.NArg() != 0 || *app == "" {
		return printErr("Invalid add ai arguments", errors.New(addAIUsage))
	}
	setup, ok := addAIProviders[*provider]
	if !ok {
		return printErr("Unknown --provider", errors.New(addAIUsage))
	}
	if !*noEnv && !addAIProductionScope(*scope) {
		// ADR 20261027132936 amendment 2: the gateway serves production
		// deployments only, so these settings would fail in any other scope.
		return printErr("The AI gateway serves production deployments only",
			fmt.Errorf("scope %q is not production; previews and other environments call the provider with their own key "+
				"(set it with gregale env set --env %s), or pass --no-env", *scope, *scope))
	}
	plan := addAIPlan{app: *app, provider: *provider, integrationID: *integrationID, name: *name, scope: *scope, setEnv: !*noEnv}
	if plan.name == "" {
		plan.name = *provider
	}
	if plan.integrationID == "" {
		var err error
		if plan.origin = *origin; plan.origin == "" {
			if plan.origin, err = setup.origin(*region); err != nil {
				return printErr("Provider origin needed", err)
			}
		}
		plan.prefixes = setup.prefixes
	}
	key, err := readAIProviderKey(*provider, setup.keyEnv, *keyEnv, *keyStdin, *keyFile, plan.integrationID != "")
	if err != nil {
		return printErr("Provider key unavailable", err)
	}
	plan.key = key
	// A key named by a flag replaces a reused integration's key; one found in
	// the provider's usual variable only keys a new integration (prod hunt
	// 2026-11-28: binding a second app replaced the key every app shared).
	plan.keyExplicit = *keyEnv != "" || *keyStdin || *keyFile != ""
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := plan.apply(context.Background(), client)
	if err != nil {
		return printErr("AI gateway setup failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	action := "Reused"
	if result.IntegrationCreated {
		action = "Created"
	}
	_, _ = fmt.Fprintf(osStdout, "%s AI integration %s (%s) and bound it to %s\n", action, result.IntegrationID, result.Provider, result.App)
	if result.KeyStored {
		_, _ = fmt.Fprintln(osStdout, "Stored the provider key; Gregale seals it and never returns it")
	} else if result.KeyKept {
		flagName := "--key-env NAME"
		if setup.keyEnv != "" {
			flagName = "--key-env " + setup.keyEnv
		}
		_, _ = fmt.Fprintf(osStdout, "Kept the integration's stored key; pass %s (or --key-stdin, --key-file) to replace it for every app it serves\n", flagName)
	}
	if result.BaseURLKept {
		_, _ = fmt.Fprintf(osStdout, "Kept the app's GREGALE_AI_BASE_URL, which may name another integration; this one is at %s\n",
			addAIProviderBaseURLKey(result.Provider))
	}
	for _, key := range sortedKeys(result.Env) {
		_, _ = fmt.Fprintf(osStdout, "Set %s=%s\n", key, result.Env[key])
	}
	_, _ = fmt.Fprintf(osStdout, "Next: gregale ai policy %s --daily-cost 25   # cap spend in USD per UTC day\n", result.IntegrationID)
	if len(result.Env) > 0 {
		_, _ = fmt.Fprintln(osStdout, "The app sees the new settings on its next deploy or restart")
		// Previews and staging inherit these settings, but the gateway
		// serves production deployments only (outbound_stage_not_allowed).
		_, _ = fmt.Fprintln(osStdout, "Previews and staging environments get 403 outbound_stage_not_allowed: give them their own provider key")
	}
	return 0
}

// readAIProviderKey reads the key as the provider stores it. Reusing an
// integration needs no key unless one is given.
func readAIProviderKey(provider, defaultEnv, env string, fromStdin bool, file string, optional bool) (string, error) {
	var raw string
	switch {
	case fromStdin:
		line, err := readDatadogAPIKey("", true)
		if err != nil {
			return "", errors.New("no key on stdin")
		}
		raw = line
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read --key-file: %w", err)
		}
		raw = strings.TrimSpace(string(data))
	case env != "":
		if raw = strings.TrimSpace(os.Getenv(env)); raw == "" {
			return "", fmt.Errorf("%s is empty", env)
		}
	case provider == api.OutboundAIProviderBedrock:
		raw = awsKeyFromEnv()
	case defaultEnv != "":
		raw = strings.TrimSpace(os.Getenv(defaultEnv))
	}
	if raw == "" {
		if optional {
			return "", nil
		}
		return "", fmt.Errorf("pass --key-env, --key-stdin or --key-file%s", map[bool]string{true: " (or set " + defaultEnv + ")"}[defaultEnv != ""])
	}
	return providerCredential(provider, raw)
}

// awsKeyFromEnv is the AWS access key in the shape Bedrock integrations
// store, from the standard AWS variables.
func awsKeyFromEnv() string {
	id, secret := strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")), strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))
	if id == "" || secret == "" {
		return ""
	}
	key := "aws:" + id + ":" + secret
	if token := strings.TrimSpace(os.Getenv("AWS_SESSION_TOKEN")); token != "" {
		key += ":" + token
	}
	return key
}

// providerCredential turns a key into the stored credential: OpenAI keys
// become bearer values, and a Vertex service-account key is put on one line.
func providerCredential(provider, raw string) (string, error) {
	switch provider {
	case api.OutboundAIProviderOpenAI:
		if !strings.HasPrefix(raw, "Bearer ") {
			raw = "Bearer " + raw
		}
	case api.OutboundAIProviderBedrock:
		if !strings.HasPrefix(raw, "aws:") {
			return "", errors.New("a Bedrock key is aws:<access-key-id>:<secret-access-key>[:<session-token>], or set the AWS_ variables")
		}
	case api.OutboundAIProviderVertex:
		if strings.HasPrefix(raw, "{") {
			var compact bytes.Buffer
			if err := json.Compact(&compact, []byte(raw)); err != nil {
				return "", fmt.Errorf("the Vertex service-account key is not JSON: %w", err)
			}
			raw = compact.String()
		}
	}
	return raw, nil
}

type addAIPlan struct {
	app, provider, integrationID, name string
	origin                             string
	prefixes                           []string
	key, scope                         string
	// keyExplicit marks a key named by --key-env, --key-stdin or
	// --key-file: only such a key replaces a reused integration's.
	keyExplicit bool
	setEnv      bool
}

// apply creates or reuses the integration, stores the key, binds the app and
// sets its SDK settings. A rerun reuses the AI integration of the same name.
func (p addAIPlan) apply(ctx context.Context, client *api.Client) (addAIResult, error) {
	result := addAIResult{App: p.app, Provider: p.provider, IntegrationID: p.integrationID}
	offers, err := client.ListOutboundIntegrationOffers(ctx)
	if err != nil {
		return result, fmt.Errorf("list integrations: %w", err)
	}
	// A reused integration whose key was never stored (an earlier run failed
	// after creating it) takes the key like a new one. An --integration ID
	// the list lacks is taken to have one.
	credentialConfigured := true
	for _, offer := range offers.Items {
		if (result.IntegrationID == "" && offer.Name == p.name && offer.AIProvider == p.provider) || offer.ID == p.integrationID {
			result.IntegrationID, credentialConfigured = offer.ID, offer.CredentialConfigured
		}
	}
	if result.IntegrationID == "" {
		created, err := client.CreateOutboundIntegration(ctx, api.CreateOutboundIntegrationRequest{
			Name: p.name, Origin: p.origin, AllowedMethods: []string{"GET", "POST"}, AllowedPathPrefixes: p.prefixes,
			AIProvider: p.provider,
		})
		if err != nil {
			return result, fmt.Errorf("create integration: %w", err)
		}
		result.IntegrationID, result.IntegrationCreated = created.ID, true
	}
	switch {
	case p.key != "" && (result.IntegrationCreated || p.keyExplicit || !credentialConfigured):
		if err := client.PutOutboundCredential(ctx, result.IntegrationID, p.key); err != nil {
			return result, fmt.Errorf("store the provider key: %w", err)
		}
		result.KeyStored = true
	case p.key != "":
		result.KeyKept = true
	}
	keepBaseURL := false
	if p.setEnv {
		keep, err := addAIKeepsBaseURL(ctx, client, p, result.IntegrationID)
		if err != nil {
			return result, err
		}
		keepBaseURL = keep
	}
	if _, err := client.BindOutboundIntegration(ctx, p.app, result.IntegrationID); err != nil {
		return result, fmt.Errorf("bind the integration to %s: %w", p.app, err)
	}
	if p.setEnv {
		result.Env = addAIEnv(p.provider, result.IntegrationID)
		if keepBaseURL {
			delete(result.Env, "GREGALE_AI_BASE_URL")
			result.BaseURLKept = true
		}
		for _, key := range sortedKeys(result.Env) {
			if err := client.SetSecretWithScope(ctx, p.app, key, result.Env[key], p.scope); err != nil {
				return result, fmt.Errorf("set %s: %w", key, err)
			}
		}
	}
	return result, nil
}

// addAIKeepsBaseURL reports whether the app keeps its GREGALE_AI_BASE_URL.
// It names one integration: an app that has it and is still bound to
// another provider's AI integration keeps it, so binding a second provider
// does not move the first one's calls (prod hunt 2026-11-28). With no such
// binding (the same provider again, or the integration it named was
// removed) it is pointed at this integration. Each provider's own
// GREGALE_AI_<PROVIDER>_BASE_URL is always set.
func addAIKeepsBaseURL(ctx context.Context, client *api.Client, p addAIPlan, integrationID string) (bool, error) {
	existing, err := client.ListSecretsWithScope(ctx, p.app, p.scope)
	if err != nil {
		return false, fmt.Errorf("list %s settings: %w", p.app, err)
	}
	if !slices.ContainsFunc(existing.Secrets, func(s api.AppSecretResponse) bool { return s.Key == "GREGALE_AI_BASE_URL" }) {
		return false, nil
	}
	bindings, err := client.ListOutboundAppBindings(ctx, p.app)
	if err != nil {
		return false, fmt.Errorf("list %s integrations: %w", p.app, err)
	}
	return slices.ContainsFunc(bindings.Items, func(b api.OutboundAppBinding) bool {
		return b.Integration.ID != integrationID && b.Integration.AIProvider != "" && b.Integration.AIProvider != p.provider
	}), nil
}

// addAIPlaceholderKey stands in for the provider key in the app: the
// gateway replaces it with the stored one.
const addAIPlaceholderKey = "gregale-managed"

// addAIEnv is the app's SDK settings for an integration: the base URL of its
// relay path and, for OpenAI and Anthropic, the variables their SDKs read.
func addAIEnv(provider, integrationID string) map[string]string {
	base := api.OutboundRelayURL + "/i/" + integrationID
	env := map[string]string{"GREGALE_AI_BASE_URL": base, addAIProviderBaseURLKey(provider): base}
	switch provider {
	case api.OutboundAIProviderOpenAI:
		env["OPENAI_BASE_URL"], env["OPENAI_API_KEY"] = base+"/v1", addAIPlaceholderKey
	case api.OutboundAIProviderAnthropic:
		env["ANTHROPIC_BASE_URL"], env["ANTHROPIC_API_KEY"] = base, addAIPlaceholderKey
	}
	return env
}

// addAIProviderBaseURLKey is the variable naming one provider's
// integration, such as GREGALE_AI_ANTHROPIC_BASE_URL.
func addAIProviderBaseURLKey(provider string) string {
	return "GREGALE_AI_" + strings.ToUpper(provider) + "_BASE_URL"
}

// addAIProductionScope reports whether the SDK settings' scope is one the
// gateway serves: the default scope or production (ADR 20261027132936
// amendment 2).
func addAIProductionScope(scope string) bool {
	return scope == "" || scope == "default" || scope == "production"
}
