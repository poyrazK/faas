package sandboxes

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// desktop turns preset desktop into an image session of the platform's
// desktop image (ADR-20261124075005 §1) and marks the body, so admission
// records the screen and exposes the views.
func (h *Handler) desktop(body *createBody) *api.Problem {
	if body.Preset != api.SandboxPresetDesktop {
		return nil
	}
	switch {
	case storagekey.SandboxDesktopImageForRef(h.DesktopImage) == "":
		return api.ErrSandboxUnsupported("preset", "sandbox desktops are not available on this platform yet")
	case body.Image != "" || body.Template != "" || body.Pull != "" || body.RegistryAuth != nil || body.Browser:
		return api.ErrValidation("preset desktop takes no image, template, pull, registry_auth or browser")
	}
	body.Preset, body.Image, body.desktop = "", h.DesktopImage, true
	return nil
}

// desktopDrive points a desktop at the platform desktop drive instead of an
// image-run job: the agent pool's imaged publishes it once for every account
// (ADR-20261124075005 §3a).
func (h *Handler) desktopDrive(a *agentstore.SandboxAdmission) {
	a.ImageRef, a.ImageJobID = h.DesktopImage, ""
	a.DesktopImageKey = storagekey.SandboxDesktopImageForRef(h.DesktopImage)
}

// validDesktop admits a desktop on a plan that has them, with its screen
// and at least SandboxDesktopRAMMBMin, and exposes its two noVNC views
// privately when previews are on (ADR-20261124075005 §1, §5). It runs after
// validPreset and validPorts. A session that is not a desktop takes no
// resolution.
func validDesktop(acct state.Account, a *agentstore.SandboxAdmission, in api.CreateSandboxRequest, desktop bool, previews bool) *api.Problem {
	if !desktop {
		if in.Resolution != "" {
			return api.ErrValidation("resolution goes with preset desktop")
		}
		return nil
	}
	idx := acct.Plan.PlanIndex()
	if !api.SandboxDesktopEnabled[idx] {
		return api.ErrAgentLimit(acct.Plan, "SandboxDesktopEnabled", 0, 1)
	}
	resolution := in.Resolution
	if resolution == "" {
		resolution = api.SandboxDesktopDefaultResolution
	}
	if _, _, ok := parseResolution(resolution); !ok {
		return api.ErrValidation(fmt.Sprintf("resolution must be WIDTHxHEIGHT from %dx%d to %dx%d; got %q",
			api.SandboxDesktopWidthMin, api.SandboxDesktopHeightMin, api.SandboxDesktopWidthMax, api.SandboxDesktopHeightMax, in.Resolution))
	}
	if in.RAMMB == 0 {
		a.RAMMB = max(a.RAMMB, api.SandboxDesktopDefaultRAMMB)
		if limit, max := ramLimit(acct.Plan, a.Kind); a.RAMMB > max {
			return api.ErrAgentLimit(acct.Plan, limit, max, a.RAMMB)
		}
	}
	if a.RAMMB < api.SandboxDesktopRAMMBMin {
		return api.ErrValidation(fmt.Sprintf("a desktop needs ram_mb of at least %d; got %d", api.SandboxDesktopRAMMBMin, a.RAMMB))
	}
	a.DesktopResolution = resolution
	if !previews {
		return nil
	}
	views := []int{api.SandboxDesktopViewPort, api.SandboxDesktopViewOnlyPort}
	for _, port := range views {
		if !hasPort(a.Ports, port) {
			a.Ports = append(a.Ports, agentstore.SandboxPort{Port: port, Visibility: api.SandboxPortPrivate})
		}
	}
	if max := api.SandboxExposedPortsMax[idx]; len(a.Ports) > max {
		return api.ErrAgentLimit(acct.Plan, "SandboxExposedPortsMax", max, len(a.Ports))
	}
	return nil
}

// parseResolution reads WIDTHxHEIGHT within the desktop's bounds.
func parseResolution(s string) (width, height int, ok bool) {
	w, h, found := strings.Cut(s, "x")
	if !found {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(w)
	height, errH := strconv.Atoi(h)
	switch {
	case errW != nil || errH != nil || strconv.Itoa(width) != w || strconv.Itoa(height) != h:
		return 0, 0, false
	case width < api.SandboxDesktopWidthMin || width > api.SandboxDesktopWidthMax:
		return 0, 0, false
	case height < api.SandboxDesktopHeightMin || height > api.SandboxDesktopHeightMax:
		return 0, 0, false
	}
	return width, height, true
}

// ramLimit names the RAM cap that applies to a session of kind and its
// value on plan.
func ramLimit(plan api.Plan, kind string) (string, int) {
	if kind == api.SandboxKindPersistent {
		return "SandboxPersistentRAMMBMax", api.SandboxPersistentRAMMBMax[plan.PlanIndex()]
	}
	return "AgentRunRAMMBMax", api.AgentRunRAMMBMax[plan.PlanIndex()]
}

func hasPort(ports []agentstore.SandboxPort, port int) bool {
	for _, p := range ports {
		if p.Port == port {
			return true
		}
	}
	return false
}
