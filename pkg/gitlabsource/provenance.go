package gitlabsource

import (
	"strconv"
	"strings"
)

// ProvenanceURL is credential-free identity, never a download URL. Both the
// immutable GitLab project ID and its verified canonical path are retained.
func ProvenanceURL(projectID int64, repository, commit string) string {
	if projectID <= 0 || !ValidRepositoryPath(repository) || !validSHA(commit) {
		return ""
	}
	return "gitlab://" + strconv.FormatInt(projectID, 10) + "/" + repository + "@" + commit
}

func MatchesProvenance(value string, projectID int64, repository, commit string) bool {
	expected := ProvenanceURL(projectID, repository, commit)
	return expected != "" && value == expected && strings.TrimSpace(value) == value
}
