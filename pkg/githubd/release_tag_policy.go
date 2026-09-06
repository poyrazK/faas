package githubd

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// Release tags are the production promotion boundary for GitHub-connected
// projects. Keep the accepted shape deliberately narrow: a tag must be a
// SemVer release with the conventional v prefix, and it may only be created
// once. A later push that moves the tag is ignored so a mutable ref cannot
// silently replace an already-promoted release.
type releaseTagRejection struct {
	reason string
	tag    string
}

func (e releaseTagRejection) Error() string {
	if e.tag == "" {
		return "githubd: release tag rejected: " + e.reason
	}
	return fmt.Sprintf("githubd: release tag %q rejected: %s", e.tag, e.reason)
}

const (
	releaseTagReasonInvalid = "invalid_release_tag"
	releaseTagReasonMoved   = "release_tag_moved"
)

// validateReleaseTag enforces the production release boundary for a tag push.
// GitHub marks a newly-created ref with `created` and supplies an all-zero
// `before` SHA. Any other shape means the tag was moved or force-updated and
// must not trigger another production deployment.
func validateReleaseTag(tag, before string, created, forced bool) error {
	if !semver.IsValid(tag) || !hasPatchComponent(tag) {
		return releaseTagRejection{reason: releaseTagReasonInvalid, tag: tag}
	}
	if !created || forced || !isZeroSHA(before) {
		return releaseTagRejection{reason: releaseTagReasonMoved, tag: tag}
	}
	return nil
}

func hasPatchComponent(tag string) bool {
	core := strings.TrimPrefix(tag, "v")
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	return strings.Count(core, ".") == 2
}

func isReleaseTagRejected(err error) bool {
	var rejection releaseTagRejection
	return errors.As(err, &rejection)
}

func releaseTagRejectReason(err error) string {
	var rejection releaseTagRejection
	if errors.As(err, &rejection) {
		return rejection.reason
	}
	return releaseTagReasonInvalid
}

func isZeroSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for i := range sha {
		if sha[i] != '0' {
			return false
		}
	}
	return true
}
