package fcvm

import "strings"

// scanNamesConfiguredBase reports whether a runtime base's scan sidecar
// describes the configured base generation (issue #299).
//
// A digest-pinned configuration must match exactly. Named nodes always run
// one: imaged refuses a tag there. An unnamed dev or single-box node may
// configure a tag such as base-minimal:latest. imaged marks the cache
// generation with that tag but records the scan under the digest the tag
// resolved to, so an exact comparison refused every cold boot once a scan
// completed. For a tag, a digest of the same repository is the configured
// generation; anything else, including another repository, still refuses.
func scanNamesConfiguredBase(scanImage, configured string) bool {
	if scanImage == configured {
		return true
	}
	if strings.Contains(configured, "@") {
		return false
	}
	repo, digest, ok := strings.Cut(scanImage, "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	return repo == repositoryOfTag(configured)
}

// repositoryOfTag strips a tag from an image reference. The tag separator is
// the last ':' after the last '/', so a registry port is kept.
func repositoryOfTag(ref string) string {
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon]
	}
	return ref
}

// ScanNamesConfiguredBase exports the boot gate's generation match, so the
// rollout's pre-stage and activation check judge a shared scan sidecar
// exactly as ensureBaseGeneration will (ADR-20261024191249).
func ScanNamesConfiguredBase(scanImage, configured string) bool {
	return scanNamesConfiguredBase(scanImage, configured)
}
