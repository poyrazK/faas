package rootfs

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

// Matches guest-init's defaultSidecarPath and absolute-only PATH lookup. Never
// consult the assembler host's PATH or execute any bytes from the image.
const defaultLaunchPATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// validateFullRootfsLaunch checks only a complete, builder-owned staging tree.
// A shared-base upper layer cannot prove whether a command exists in its base.
// commandPATH is nil when wake-time PATH is unknown (for example, sealed).
// This validates file presence/type/mode, not ELF loaders, script interpreters,
// effective user permissions, shell command strings, or application readiness.
func validateFullRootfsLaunch(root string, m api.AppManifest, commandPATH *string) error {
	cwd := m.EffectiveWorkingDir()
	// Do not validate cwd independently: guest-init may create it as a runtime
	// mount target (for example /tmp or an ephemeral shared volume).
	command := m.Entrypoint[0] // BuildFullRootfs has already validated the manifest.
	if strings.Contains(command, "/") {
		candidate := command
		if !path.IsAbs(candidate) {
			// Do not clean before resolving: symlink/.. follows guest traversal.
			candidate = cwd + "/" + candidate
		}
		if reason := imageExecutableProblem(root, candidate); reason != "" {
			return invalidImageLaunch("entrypoint", command, reason)
		}
		return nil
	}
	if commandPATH == nil {
		return nil // Cannot prove a PATH-selected command is missing at wake.
	}
	value := *commandPATH
	if value == "" {
		value = defaultLaunchPATH
	}
	hasAbsoluteDir := false
	for _, dir := range strings.Split(value, ":") {
		if !path.IsAbs(dir) {
			continue // guest-init also ignores empty and relative PATH entries.
		}
		hasAbsoluteDir = true
		if imageExecutableProblem(root, path.Join(path.Clean(dir), command)) == "" {
			return nil
		}
	}
	// With no absolute PATH directories, guest-init returns /<command>.
	if !hasAbsoluteDir && imageExecutableProblem(root, "/"+command) == "" {
		return nil
	}
	return invalidImageLaunch("entrypoint", command, "has no executable in the effective image PATH")
}

func imageExecutableProblem(root, candidate string) string {
	// Guest mounts replace these image paths before app launch. They cannot
	// be validated from OCI layers (for example /proc/self/exe), even through
	// an image symlink. Resolve in-root without requiring mounted files first.
	if resolved, err := resolveWithin(root, candidate); err == nil && runtimeLaunchPath(root, resolved) {
		return ""
	}
	resolved, err := resolveExistingWithin(root, candidate)
	if err != nil {
		return "cannot be resolved inside the image (missing path or invalid symlink)"
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "cannot be inspected inside the image"
	}
	if !info.Mode().IsRegular() || strings.HasSuffix(candidate, "/") {
		return "is not a regular executable file in the image"
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "has no execute permission in the image; set an executable mode when building the image"
	}
	return ""
}

func runtimeLaunchPath(root, resolved string) bool {
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	guestPath := "/" + filepath.ToSlash(rel)
	for _, mount := range []string{"/dev", "/proc", "/sys", "/tmp", api.FullRootfsSidecarMountPath} {
		if guestPath == mount || strings.HasPrefix(guestPath, mount+"/") {
			return true
		}
	}
	return false
}

func invalidImageLaunch(field, value, reason string) error {
	// Include only the selected command/cwd, never host staging paths, full argv,
	// PATH values, environment values, or raw filesystem errors.
	return fmt.Errorf("%w: rootfs launch: %s %q %s", oci.ErrImageManifestInvalid, field, value, reason)
}
