package e2etest

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime/debug"

	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
)

// PostgresGuestRuntimeManifest fingerprints the files the isolated harness
// actually uses. Cached Go binaries must carry this clean checkout's revision.
func PostgresGuestRuntimeManifest(paths map[string]string, revision string, complete bool) (guestqualification.RuntimeManifest, error) {
	result := guestqualification.RuntimeManifest{Version: guestqualification.Version, SourceRevision: revision, Artifacts: make(map[string]string)}
	if len(paths) > len(guestqualification.SuiteContract().Artifacts) {
		return result, errors.New("unexpected guest qualification artifact")
	}
	for _, name := range guestqualification.SuiteContract().Artifacts {
		path := paths[name]
		if !complete && path == "" && (name == "probe_base" || name == "node22_base") {
			continue
		}
		if path == "" {
			return result, errors.New("missing guest qualification artifact")
		}
		if name != "kernel" && name != "builder_base" && name != "probe_base" && name != "node22_base" && name != "firecracker" && name != "jailer" && name != "e2e_test" {
			info, err := buildinfo.ReadFile(path)
			if err != nil || !postgresGuestBinaryRevision(info, revision) {
				return result, errors.New("guest qualification binary revision mismatch")
			}
		}
		digest, err := PostgresGuestArtifactDigest(path)
		if err != nil {
			return result, err
		}
		result.Artifacts[name] = digest
	}
	if complete && !result.Valid() {
		return result, errors.New("guest qualification manifest invalid")
	}
	return result, nil
}

func PostgresGuestArtifactDigest(path string) (string, error) {
	pathInfo, err := os.Stat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return "", errors.New("guest qualification artifact invalid")
	}
	// Paths name the acceptance harness's own fixture files; only hashes escape.
	f, err := os.Open(path) //nolint:forbidigo
	if err != nil {
		return "", errors.New("guest qualification artifact unreadable")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		_ = f.Close()
		return "", errors.New("guest qualification artifact invalid")
	}
	digest := sha256.New()
	_, err = io.Copy(digest, f)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return "", errors.New("guest qualification artifact unreadable")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func postgresGuestBinaryRevision(info *debug.BuildInfo, revision string) bool {
	if info == nil {
		return false
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings["vcs"] == "git" && settings["vcs.revision"] == revision && settings["vcs.modified"] == "false" && settings["GOOS"] == "linux" && settings["GOARCH"] == "amd64"
}
