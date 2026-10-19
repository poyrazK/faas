// adr: 1747 — jobs retain their own identity; an app UUID is never invented.
package financial

import (
	"strconv"

	"github.com/google/uuid"
)

// ComputeScopeKey is an internal book selector, not a customer resource ID.
// Existing app keys remain unchanged; the prefix prevents cross-kind aliases.
func ComputeScopeKey(s BudgetScope) string {
	if s.Kind == "job" {
		return "job:" + s.ID
	}
	return s.ID
}

func computeWorkloadKey(app, job string) string {
	if job != "" {
		return "job:" + job
	}
	return app
}

func (r ComputeRequest) WorkloadKey() string { return computeWorkloadKey(r.AppID, r.JobID) }
func (l ComputeLease) WorkloadKey() string   { return computeWorkloadKey(l.AppID, l.JobID) }
func (b ComputeBook) WorkloadKey() string    { return computeWorkloadKey(b.AppID, b.JobID) }

// ComputeJobAttemptID binds an opaque scheduler lease token to one partition.
// It is stable across lost RPC replies, and changes for every retry claim.
func ComputeJobAttemptID(run string, index int, token string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("financial-job-attempt\n"+run+"\n"+strconv.Itoa(index)+"\n"+token)).String()
}
