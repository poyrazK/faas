package tasktoolrehearsal

import "github.com/google/uuid"

const MigrationWorkload = "migration"

// WorkloadID derives an app identity from the native run reservation.
func WorkloadID(run uuid.UUID, role string) string {
	return uuid.NewSHA1(run, []byte("hosted-task-rehearsal-v1:"+role)).String()
}
