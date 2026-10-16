package state

// adr: 435. Keep plaintext intent out of the producer; retain its canonical hash.

import (
	"bytes"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

type sourceBuildRootfsIntent struct {
	Slug                   string                `json:"slug"`
	Type                   AppType               `json:"type"`
	Runtime                string                `json:"runtime"`
	StartCommand           string                `json:"start_command"`
	Manifest               AppManifest           `json:"manifest"`
	RequireSigned          bool                  `json:"require_signed"`
	SecurityPolicy         api.AppSecurityPolicy `json:"security_policy"`
	Handler                string                `json:"handler"`
	Scope                  string                `json:"scope"`
	OverrideEntrypoint     []string              `json:"override_entrypoint"`
	OverrideCmd            []string              `json:"override_cmd"`
	OverrideEnv            json.RawMessage       `json:"override_env"`
	OverrideEnvSecrets     json.RawMessage       `json:"override_env_secrets"`
	OverridePort           int                   `json:"override_port"`
	OverrideHealthcheck    json.RawMessage       `json:"override_healthcheck"`
	OverrideLivenessProbe  json.RawMessage       `json:"override_liveness_probe"`
	OverrideReadinessProbe json.RawMessage       `json:"override_readiness_probe"`
	OverrideMainDependsOn  json.RawMessage       `json:"override_main_depends_on"`
}

func SourceBuildRootfsIntentHash(app App, dep Deployment) (string, error) {
	return hashSourceBuildRootfsIntent(sourceBuildRootfsOwnerIntent(app, dep))
}

func sourceBuildRootfsOwnerIntent(app App, dep Deployment) sourceBuildRootfsIntent {
	return sourceBuildRootfsIntent{Slug: app.Slug, Type: app.Type, Runtime: app.Runtime, StartCommand: app.StartCommand,
		Manifest: app.Manifest, RequireSigned: app.RequireSigned, SecurityPolicy: app.SecurityPolicy, Handler: dep.Handler, Scope: dep.Scope,
		OverrideEntrypoint: dep.OverrideEntrypoint, OverrideCmd: dep.OverrideCmd, OverrideEnv: dep.OverrideEnv,
		OverrideEnvSecrets: dep.OverrideEnvSecrets, OverridePort: dep.OverridePort, OverrideHealthcheck: dep.OverrideHealthcheck,
		OverrideLivenessProbe: dep.OverrideLivenessProbe, OverrideReadinessProbe: dep.OverrideReadinessProbe, OverrideMainDependsOn: dep.OverrideMainDependsOn}
}

func hashSourceBuildRootfsIntent(in sourceBuildRootfsIntent) (string, error) {
	if in.Type == "" {
		in.Type = AppTypeApp
	}
	if len(in.OverrideEntrypoint) == 0 {
		in.OverrideEntrypoint = nil
	}
	if len(in.OverrideCmd) == 0 {
		in.OverrideCmd = nil
	}
	if len(in.OverrideMainDependsOn) == 0 {
		in.OverrideMainDependsOn = json.RawMessage("[]")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	return standardReviewDigest(value)
}
