package runtimequota

// adr: 653
import "github.com/google/uuid"

// AppTaskInstanceID gives each durable lease attempt its own native lookup key.
// A retry of the same claim is stable; a replacement lease never shares a VM.
// This identity is not execution authority or evidence of physical absence.
func AppTaskInstanceID(taskID, leaseToken string) (string, error) {
	if !validID(taskID) || !validID(leaseToken) {
		return "", ErrInvalid
	}
	return uuid.NewSHA1(uuid.Nil, []byte("gregale/app-task-instance/v1/"+taskID+"/"+leaseToken)).String(), nil
}
