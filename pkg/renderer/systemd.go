package renderer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// renderSystemd returns the bytes for the per-daemon systemd unit file
// at /etc/systemd/system/faas-<daemon>.service. The renderer does NOT
// construct the daemonunit.Unit — pkg/daemonunitspec.UnitXxx() is the
// single source of truth for the per-daemon directives. The renderer
// only routes the call to the correct constructor and serialises via
// pkg/daemonunit.Unit.Render().
//
// The 8 daemonunitspec.UnitXxx() functions live in
// pkg/daemonunitspec/{vmmd,apid,schedd,gatewayd_internal,
// gatewayd_public,meterd,githubd,imaged}.go. The renderer looks the
// daemon up by name (the manifest's daemon name with `_` → `-`
// flipped for the two gatewayd daemons, matching the cli convention).
//
// hostName is the value of $FAAS_NODE_NAME the renderer threads into
// the rendered unit. Empty hostName means "single-box dev" — the
// Environment= line is omitted so the loader's TOML default applies
// (the daemon will refuse to self-register against a multi-host
// compute_nodes row that doesn't exist). PR-2 of issue #911 / ADR-110
// threads host.Name through here so a multi-host render emits a
// unit that boots into the right compute_nodes row.
func renderSystemd(daemon, hostName string) ([]byte, error) {
	unit, err := daemonunitspec.UnitByName(daemon)
	if err != nil {
		return nil, fmt.Errorf("renderer: %s: %w", daemon, err)
	}
	body := unit.Render()
	if hostName == "" {
		return body, nil
	}
	return injectNodeNameEnvironment(body, hostName)
}

// injectNodeNameEnvironment appends an `Environment=FAAS_NODE_NAME=<hostName>`
// line to the [Service] block of `body`. The line is inserted after the
// last `Environment=` / `EnvironmentFile=` directive in that block
// (preserving the daemonunit.Unit field ordering — daemonunit emits
// directives in a deterministic order, and the new line belongs at the tail
// of the directive cluster, not a fresh one).
//
// The function is a no-op on bodies that already carry the
// FAAS_NODE_NAME environment line (idempotent against re-renders that
// race the unit file write). The replacement is line-based
// (bytes.Replace, not regex) because the daemonunit output is plain
// INI-shaped text and a regex would be overkill for a single-line
// suffix.
func injectNodeNameEnvironment(body []byte, hostName string) ([]byte, error) {
	const marker = "Environment=FAAS_NODE_NAME="
	if bytes.Contains(body, []byte(marker)) {
		return body, nil
	}

	lines := strings.Split(string(body), "\n")
	serviceStart := -1
	serviceEnd := len(lines)
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "[Service]" {
			serviceStart = i
			break
		}
	}
	if serviceStart < 0 {
		return nil, fmt.Errorf("systemd unit has no [Service] section")
	}
	for i := serviceStart + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			serviceEnd = i
			break
		}
	}

	// Insert after the last environment directive in [Service]. If the
	// constructor has no environment directives, place it at the start of
	// the section. Restricting the scan to [Service] is important: systemd
	// ignores Environment= in [Unit], which would silently drop node identity.
	insertAt := serviceStart + 1
	for i := serviceStart + 1; i < serviceEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "Environment=") || strings.HasPrefix(trimmed, "EnvironmentFile=") {
			insertAt = i + 1
		}
	}
	newLine := "Environment=FAAS_NODE_NAME=" + hostName

	// Build the new body from logical lines. The trailing empty element from
	// Split preserves daemonunit.Render's final newline.
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, newLine)
	out = append(out, lines[insertAt:]...)
	return []byte(strings.Join(out, "\n")), nil
}

// renderSliceUnit returns the bytes for /etc/systemd/system/faas-cp.slice.
// The slice is emitted once per host (it is the wrapper for all 8
// daemons, not a daemon itself). pkg/daemonunitspec.UnitSlice() is the
// source of truth for the field values.
//
// The renderer calls RenderSlice() (NOT Render()) because slice units
// must use the [Slice] section for MemoryMax — Render() emits
// [Service] which silently drops the control-plane memory ceiling. Per daemonunit's
// godoc on RenderSlice(), this is the load-bearing invariant for
// tenant admission.
func renderSliceUnit() []byte {
	return daemonunitspec.UnitSlice().RenderSlice()
}
