package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/state"
)

func isDirectOCIImage(app state.App, dep state.Deployment) bool {
	return dep.Kind == state.DeploymentKindImage && app.Type != state.AppTypeFunction && strings.TrimSpace(app.Runtime) == ""
}

func hostingReceiptProfile(app state.App, dep state.Deployment) frameworkprofile.Profile {
	// A direct OCI image is a normal HTTP container, not a Gregale runner.
	// Unless it carries an explicit override, its only readiness contract is
	// accepting connections on the advertised port. An empty HealthPath is
	// persisted deliberately: sched treats it as the legacy TCP gate instead
	// of inventing a /healthz endpoint the image never promised.
	healthPath := "/healthz"
	if isDirectOCIImage(app, dep) {
		healthPath = ""
	}
	profile := frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: string(markers.FrameworkUnknown), Port: api.DefaultAppPort, HealthPath: healthPath}
	if persisted, ok := persistedProfile(dep); ok {
		profile = persisted
	}
	if profile.Port <= 0 {
		profile.Port = api.DefaultAppPort
	}
	if profile.HealthPath == "" && !isDirectOCIImage(app, dep) {
		profile.HealthPath = "/healthz"
	}
	if app.Manifest.Port > 0 {
		profile.Port = app.Manifest.Port
	}
	// Image manifests are seeded with /healthz for the guest contract even
	// when the OCI image declared no HTTP endpoint. Do not mistake that
	// platform default for an explicit direct-OCI readiness choice; a
	// non-default app value remains an intentional override.
	if app.Manifest.Healthz != "" && (!isDirectOCIImage(app, dep) || app.Manifest.Healthz != defaultHealthzPath) {
		profile.HealthPath = app.Manifest.Healthz
	}
	if len(dep.OverrideHealthcheck) > 0 {
		var check struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(dep.OverrideHealthcheck, &check); err == nil && check.Path != "" {
			profile.HealthPath = check.Path
		}
	}
	if start := strings.TrimSpace(app.StartCommand); start != "" {
		profile.StartCommand = start
	} else if len(app.Manifest.Entrypoint) > 0 {
		profile.StartCommand = strings.Join(app.Manifest.Entrypoint, " ")
	}
	if dep.OverridePort > 0 {
		profile.Port = dep.OverridePort
	}
	if len(dep.InferredProfile) == 0 && dep.SourcePath != "" {
		if _, err := os.Stat(dep.SourcePath); err == nil {
			if fw, err := markers.DetectFromTarballAtRoot(dep.SourcePath, dep.SourceRoot); err == nil {
				profile.Framework = string(fw)
				profile.FrameworkVer = markers.VersionFromTarballAtRoot(dep.SourcePath, fw, dep.SourceRoot)
				profile.Inferred = fw != markers.FrameworkUnknown
			}
		}
	}
	return profile
}

// HostingHealthPath returns the effective public smoke path used by the
// deployment receipt and smoke verifier. Direct OCI boot readiness is a TCP
// listener gate, so the public smoke still uses the conventional root path
// instead of falling back to the nonexistent /healthz endpoint.
func HostingHealthPath(app state.App, dep state.Deployment) string {
	path := hostingReceiptProfile(app, dep).HealthPath
	if path == "" && isDirectOCIImage(app, dep) {
		return "/"
	}
	return path
}

func buildHostingReceipt(app state.App, dep state.Deployment, smoke apihostingreceipt.SmokeResult) apihostingreceipt.Receipt {
	return apihostingreceipt.Receipt{
		SchemaVersion: apihostingreceipt.SchemaVersion,
		DeploymentID:  dep.ID,
		AppID:         app.ID,
		AppURL:        hostingAppURL(app.Slug),
		Source:        apihostingreceipt.Source{Kind: string(dep.Kind), URL: safeSourceURL(dep.SourceURL), CommitSHA: dep.CommitSHA, ImageDigest: dep.ImageDigest},
		Profile:       hostingReceiptProfile(app, dep),
		Artifact:      apihostingreceipt.Artifact{RootfsKey: dep.RootfsKey, RootfsBytes: dep.RootfsBytes},
		Smoke:         smoke,
	}
}

func hostingAppURL(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	domain := strings.Trim(strings.TrimSpace(os.Getenv("FAAS_APPS_DOMAIN")), ".")
	if domain == "" || domain == "apps.gregale.dev" {
		domain = "gregale.dev"
	}
	return "https://" + slug + "." + domain
}

func safeSourceURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (h *Handler) persistHostingReceipt(ctx context.Context, app state.App, dep state.Deployment, smoke apihostingreceipt.SmokeResult) error {
	store, ok := h.store.(state.DeploymentHostingReceiptStore)
	if !ok {
		return nil
	}
	raw, err := apihostingreceipt.Encode(buildHostingReceipt(app, dep, smoke))
	if err != nil {
		return fmt.Errorf("encode hosting receipt: %w", err)
	}
	if _, err := store.UpsertDeploymentHostingReceipt(ctx, dep.ID, raw); err != nil {
		return fmt.Errorf("persist hosting receipt: %w", err)
	}
	return nil
}

func hostingSmokeFailure(smoke apihostingreceipt.SmokeResult) error {
	if smoke.Status != apihostingreceipt.SmokeFailed {
		return nil
	}
	if smoke.Error != "" {
		return errors.New(smoke.Error)
	}
	if smoke.ErrorCode != "" {
		return errors.New(smoke.ErrorCode)
	}
	return errors.New("post-readiness smoke failed")
}
