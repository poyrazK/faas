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

func hostingReceiptProfile(app state.App, dep state.Deployment) frameworkprofile.Profile {
	profile := frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: string(markers.FrameworkUnknown), Port: api.DefaultAppPort, HealthPath: "/healthz"}
	if app.Manifest.Port > 0 {
		profile.Port = app.Manifest.Port
	}
	if app.Manifest.Healthz != "" {
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
	profile.StartCommand = strings.TrimSpace(app.StartCommand)
	if profile.StartCommand == "" {
		profile.StartCommand = strings.Join(app.Manifest.Entrypoint, " ")
	}
	if dep.OverridePort > 0 {
		profile.Port = dep.OverridePort
	}
	if dep.SourcePath != "" {
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

// HostingHealthPath returns the effective public readiness path used by the
// deployment receipt and smoke verifier.
func HostingHealthPath(app state.App, dep state.Deployment) string {
	return hostingReceiptProfile(app, dep).HealthPath
}

func buildHostingReceipt(app state.App, dep state.Deployment, smoke apihostingreceipt.SmokeResult) apihostingreceipt.Receipt {
	return apihostingreceipt.Receipt{
		SchemaVersion: apihostingreceipt.SchemaVersion,
		DeploymentID:  dep.ID,
		AppID:         app.ID,
		Source:        apihostingreceipt.Source{Kind: string(dep.Kind), URL: safeSourceURL(dep.SourceURL), CommitSHA: dep.CommitSHA, ImageDigest: dep.ImageDigest},
		Profile:       hostingReceiptProfile(app, dep),
		Artifact:      apihostingreceipt.Artifact{RootfsKey: dep.RootfsKey, RootfsBytes: dep.RootfsBytes},
		Smoke:         smoke,
	}
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
