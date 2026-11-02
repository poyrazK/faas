package api

// GitHubSkipReasonDescription explains why a GitHub delivery Gregale
// processed deployed nothing (ADR-20261101160300, amendment 2), or returns ""
// for a reason it does not know.
func GitHubSkipReasonDescription(reason string) string {
	switch reason {
	case "actions_trigger":
		return "Production deploys come from your GitHub Actions workflow; if nothing deploys, commit and push it."
	case "branch_not_deployed":
		return "Nothing deploys this branch."
	case "superseded":
		return "A newer commit reached the branch first and deploys instead."
	case "not_enabled":
		return "Deploys from GitHub are not enabled for this account yet."
	case "tag_not_branch_head":
		return "The release tag is not on the head of a branch an app deploys."
	case "no_binding":
		return "No app is bound to this repository."
	case "skip_marker":
		return "The commit message asked to skip the deploy."
	case "invalid_release_tag":
		return "The tag is not a release tag (vMAJOR.MINOR.PATCH)."
	case "release_tag_moved":
		return "The release tag already existed or was moved."
	case "ignored":
		return "Gregale had nothing to do for this event."
	}
	return ""
}
