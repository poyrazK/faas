package releaseinstall

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// supportBinaryNames are executable files that are not daemons but are
// required by a running host or by the guest image builder. They travel with
// the atomic release because vmmd starts the bridge helpers, imaged injects
// init as the guest PID 1, and the upgrade path invokes gregalectl from the
// active release tree.
var supportBinaryNames = []string{
	"gregale",
	"gregalectl",
	"init",
	"schedd-brokerq-apply",
	"vmmd-jail-helper",
	"vmmd-raw-bridge",
	"vmmd-stream-bridge",
	// vmlinux is the release-pinned Firecracker guest kernel. It is kept in
	// the signed bundle so compute hosts never rebuild a host-specific kernel
	// during a production join.
	"vmlinux",
}

// legacyUnhashedSupportBinaryNames contains support files that appeared in
// pre-canonical release directories before they were added to tool_hashes.
// Keep this list deliberately narrow: these files may be preserved during a
// retry of an older signed release, but arbitrary extra files must still fail
// catalog verification.
var legacyUnhashedSupportBinaryNames = []string{"init"}

// runtimeAssetNames is the immutable set of guest function runners that
// imaged injects into application layers. They are release assets, not
// daemons: keeping them inside the same release directory makes the runner
// paths atomic with the imaged binary and prevents a rollback from pairing
// old code with a new runner ABI.
var runtimeAssetNames = []string{
	"runners/go124/faas-runner",
	"runners/go124-alpine/faas-runner",
	"runners/node22/faas-runner",
	"runners/node24/faas-runner",
	"runners/python312/faas-runner",
	"runners/python313/faas-runner",
}

// SupportBinaryNames returns the fixed support-binary catalog in stable order.
func SupportBinaryNames() []string {
	return append([]string(nil), supportBinaryNames...)
}

// LegacyUnhashedSupportBinaryNames returns the fixed compatibility catalog for
// release manifests produced before the support-binary hash map included the
// guest PID 1 binary.
func LegacyUnhashedSupportBinaryNames() []string {
	return append([]string(nil), legacyUnhashedSupportBinaryNames...)
}

// IsLegacyUnhashedSupportBinaryName reports whether name is a known support
// file that may exist in an older release directory without a tool_hashes
// entry. It is not a general-purpose extra-file allowlist.
func IsLegacyUnhashedSupportBinaryName(name string) bool {
	for _, candidate := range legacyUnhashedSupportBinaryNames {
		if name == candidate {
			return true
		}
	}
	return false
}

// RuntimeAssetNames returns the stable release-relative paths for all
// function-runner assets.
func RuntimeAssetNames() []string {
	return append([]string(nil), runtimeAssetNames...)
}

// IsRuntimeAssetName reports whether name is one of the release's approved
// nested runtime assets.
func IsRuntimeAssetName(name string) bool {
	for _, candidate := range runtimeAssetNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func sortedRuntimeAssetNames() []string {
	assets := RuntimeAssetNames()
	sort.Strings(assets)
	return assets
}

// executableName translates the manifest's logical daemon key to the
// filename emitted by the Go build and consumed by systemd. The manifest
// uses underscores for YAML/TOML map keys, while the two gateway binaries
// use dashes everywhere else in the deployment tree.
func executableName(logical string) string {
	switch logical {
	case "gatewayd_internal":
		return "gatewayd-internal"
	case "gatewayd_public":
		return "gatewayd-public"
	default:
		return logical
	}
}

// IsCatalogBinaryName reports whether name is either a logical catalog key
// or the executable filename for a catalog daemon. Release bundle inputs may
// come from the canonical daemon build (dashed gateway names) or from the
// package-level tests/older tooling (logical underscored names).
func IsCatalogBinaryName(name string) bool {
	for _, logical := range manifest.SortedHostKeys() {
		if name == logical || name == executableName(logical) {
			return true
		}
	}
	return false
}

// IsReleaseBinaryName reports whether name is a daemon or a required support
// executable in the immutable release bundle.
func IsReleaseBinaryName(name string) bool {
	if IsCatalogBinaryName(name) {
		return true
	}
	for _, support := range supportBinaryNames {
		if name == support {
			return true
		}
	}
	return false
}

// resolveBinary returns the on-disk file for a logical daemon key. It
// accepts the historical logical filename as a compatibility path, but
// prefers the canonical executable filename when that is the only one
// present. This keeps the manifest keys stable while making the release
// bundle agree with systemd and the build scripts.
func resolveBinary(bin, logical string) (string, error) {
	candidates := []string{logical}
	if canonical := executableName(logical); canonical != logical {
		candidates = append(candidates, canonical)
	}
	var firstErr error
	for _, candidate := range candidates {
		path := filepath.Join(bin, candidate)
		info, err := os.Stat(path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return "", &os.PathError{Op: "stat", Path: path, Err: os.ErrInvalid}
			}
			return path, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return filepath.Join(bin, candidates[0]), firstErr
}
