// Package simpleapp contains the opinionated deployment contract for the
// customer-facing stateless application path. It deliberately stays above
// the guest AppManifest contract: callers describe an app intent and the
// deploy adapters compile that intent into the existing API request.
package simpleapp

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// SourceKind identifies the source a simple app will be built from.
type SourceKind string

const (
	SourceDirectory SourceKind = "directory"
	SourceImage     SourceKind = "image"
)

// Spec is the small, user-facing set of deployment inputs. Empty values use
// the platform defaults; advanced lifecycle modes intentionally do not belong
// in this contract.
type Spec struct {
	Slug       string
	Source     SourceKind
	Framework  string
	Profile    string
	Port       int
	HealthPath string
}

// Plan is the resolved stateless application contract shown by the CLI. The
// state fields are explicit so users understand that local disk is disposable
// and durable state must be supplied through a binding or an external service.
type Plan struct {
	Slug            string     `json:"slug"`
	Source          SourceKind `json:"source"`
	Framework       string     `json:"framework,omitempty"`
	ResourceProfile string     `json:"resource_profile"`
	MemoryMB        int        `json:"memory_mb,omitempty"`
	CPUMillicores   int        `json:"cpu_millicores,omitempty"`
	Port            int        `json:"port"`
	HealthPath      string     `json:"health_path"`
	ExecutionMode   string     `json:"execution_mode"`
	ScaleToZero     bool       `json:"scale_to_zero"`
	LocalStorage    string     `json:"local_storage"`
	DurableState    string     `json:"durable_state"`
	DefaultsApplied []string   `json:"defaults_applied"`
}

// CreateRequest compiles the resolved intent into Gregale's existing app
// creation DTO. The plan-default profile is intentionally omitted so the API
// can apply the account plan's resource shape without duplicating that policy
// in the CLI.
func (p Plan) CreateRequest() api.CreateAppRequest {
	req := api.CreateAppRequest{
		Slug:          p.Slug,
		Type:          "app",
		ExecutionMode: p.ExecutionMode,
		HealthPath:    p.HealthPath,
	}
	if p.ResourceProfile != "" && p.ResourceProfile != "plan-default" {
		req.ResourceProfile = p.ResourceProfile
	}
	return req
}

const (
	DefaultPort           = api.DefaultAppPort
	DefaultHealthPath     = "/healthz"
	LocalStorageEphemeral = "ephemeral"
	DurableStateExternal  = "external"
)

// Resolve validates a simple app intent and fills the safe stateless defaults.
// It does not contact the API or inspect the filesystem, which makes it safe
// for previews, local tooling, and future dashboard clients to share.
func Resolve(spec Spec) (Plan, error) {
	slug := strings.TrimSpace(spec.Slug)
	if !api.ValidAppSlug(slug) {
		return Plan{}, fmt.Errorf("slug must be 3–40 chars, lowercase letters, digits, and hyphens")
	}
	source := spec.Source
	if source == "" {
		source = SourceDirectory
	}
	if source != SourceDirectory && source != SourceImage {
		return Plan{}, fmt.Errorf("source must be %q or %q", SourceDirectory, SourceImage)
	}
	profile := strings.TrimSpace(spec.Profile)
	var profileSpec api.ResourceProfileSpec
	if profile != "" {
		var ok bool
		profileSpec, ok = api.ResourceProfileSpecFor(profile)
		if !ok {
			return Plan{}, fmt.Errorf("resource profile must be one of: micro, small, medium, large, xlarge")
		}
	}
	port := spec.Port
	if port == 0 {
		port = DefaultPort
	}
	if port < 1 || port > 65535 {
		return Plan{}, fmt.Errorf("port must be between 1 and 65535")
	}
	health := strings.TrimSpace(spec.HealthPath)
	if health == "" {
		health = DefaultHealthPath
	}
	if !strings.HasPrefix(health, "/") || strings.ContainsAny(health, "\x00\r\n") {
		return Plan{}, fmt.Errorf("health path must start with '/' and contain no control characters")
	}
	if len(health) > 1024 {
		return Plan{}, fmt.Errorf("health path must be at most 1024 characters")
	}
	framework := strings.TrimSpace(spec.Framework)
	defaults := []string{"execution_mode=request", "scale_to_zero=true", "local_storage=ephemeral", "durable_state=external"}
	if profile == "" {
		profile = "plan-default"
		defaults = append(defaults, "resource_profile=plan-default")
	}
	if spec.Port == 0 {
		defaults = append(defaults, fmt.Sprintf("port=%d", port))
	}
	if strings.TrimSpace(spec.HealthPath) == "" {
		defaults = append(defaults, "health_path="+health)
	}
	return Plan{
		Slug:            slug,
		Source:          source,
		Framework:       framework,
		ResourceProfile: profile,
		MemoryMB:        profileSpec.MemoryMB,
		CPUMillicores:   profileSpec.CPUMillicores,
		Port:            port,
		HealthPath:      health,
		ExecutionMode:   api.ExecutionModeRequest,
		ScaleToZero:     true,
		LocalStorage:    LocalStorageEphemeral,
		DurableState:    DurableStateExternal,
		DefaultsApplied: defaults,
	}, nil
}
