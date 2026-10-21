// adr: 1747 — jobs retain their own identity; an app UUID is never invented.
package financial

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ComputeScopeKey is an internal book selector, not a customer resource ID.
// Existing app keys remain unchanged; the prefix prevents cross-kind aliases.
func ComputeScopeKey(s BudgetScope) string {
	switch s.Kind {
	case "job":
		return "job:" + s.ID
	case "project":
		return ComputeProjectKey(s.ID)
	case "environment":
		return ComputeEnvironmentKey(s.ID)
	}
	return s.ID
}

// ComputeProjectKey selects a shared project book (ADR-1750, amendment 1).
func ComputeProjectKey(project string) string { return "project:" + project }

// ComputeEnvironmentKey selects a shared environment book (ADR-1750,
// amendment 2).
func ComputeEnvironmentKey(environment string) string { return "environment:" + environment }

func computeWorkloadKey(app, job string) string {
	if job != "" {
		return "job:" + job
	}
	return app
}

func (r ComputeRequest) WorkloadKey() string { return computeWorkloadKey(r.AppID, r.JobID) }
func (l ComputeLease) WorkloadKey() string   { return computeWorkloadKey(l.AppID, l.JobID) }
func (b ComputeBook) WorkloadKey() string    { return computeWorkloadKey(b.AppID, b.JobID) }

// ScopeKey is the book's selector: a project or environment key for a
// shared book, else its workload key ("" for the account book).
func (b ComputeBook) ScopeKey() string {
	switch {
	case b.ProjectID != "":
		return ComputeProjectKey(b.ProjectID)
	case b.EnvironmentID != "":
		return ComputeEnvironmentKey(b.EnvironmentID)
	}
	return b.WorkloadKey()
}

// ComputeProjectID reverses ComputeProjectKey.
func ComputeProjectID(key string) (string, bool) { return strings.CutPrefix(key, "project:") }

// ComputeEnvironmentID reverses ComputeEnvironmentKey.
func ComputeEnvironmentID(key string) (string, bool) { return strings.CutPrefix(key, "environment:") }

// ComputeJobAttemptID binds an opaque scheduler lease token to one partition.
// It is stable across lost RPC replies, and changes for every retry claim.
func ComputeJobAttemptID(run string, index int, token string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("financial-job-attempt\n"+run+"\n"+strconv.Itoa(index)+"\n"+token)).String()
}
