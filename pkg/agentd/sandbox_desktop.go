package agentd

import (
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// The desktop's commands (ADR-20261124075005 §2): gregale-desktop in the
// platform desktop image starts Xvfb, Xfce, x11vnc and noVNC at the
// session's screen; its wait polls them. They are agentd's constants and
// travel as a template's start and ready commands, so schedd and vmmd run
// them unchanged. The resolution is the only input, and it is digits.
const (
	sandboxDesktopStartCmd = "gregale-desktop start --resolution "
	sandboxDesktopReadyCmd = "gregale-desktop wait"
)

// desktopResolutionRE is the sandbox_sessions.desktop_resolution check.
var desktopResolutionRE = regexp.MustCompile(`^[1-9][0-9]{2,3}x[1-9][0-9]{2,3}$`)

// desktopCommands returns a desktop session's start and ready commands and
// ready timeout, or ok false for any other session. A desktop needs its
// image: without one it boots no desktop, and the claim's own commands
// (none) stand.
func desktopCommands(claim agentstore.SandboxClaim) (start, ready string, timeout time.Duration, ok bool) {
	if claim.DesktopResolution == "" || claim.ImageKey == "" || !desktopResolutionRE.MatchString(claim.DesktopResolution) {
		return "", "", 0, false
	}
	return sandboxDesktopStartCmd + claim.DesktopResolution, sandboxDesktopReadyCmd,
		time.Duration(api.SandboxDesktopReadyTimeoutSec) * time.Second, true
}
