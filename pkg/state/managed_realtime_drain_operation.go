package state

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrManagedRealtimeDrainOperationNotFound = errors.New("state: managed realtime drain operation not found")

type ManagedRealtimeDrainOperationStatus string

const (
	ManagedRealtimeDrainOperationRunning   ManagedRealtimeDrainOperationStatus = "running"
	ManagedRealtimeDrainOperationCompleted ManagedRealtimeDrainOperationStatus = "completed"
	ManagedRealtimeDrainOperationPartial   ManagedRealtimeDrainOperationStatus = "partial"
)

// ManagedRealtimeDrainOperation is the durable control-plane handle for one
// bounded or all-matching connection drain. Result contains the complete API
// response so the operation can be inspected after the live owner has
// forgotten the sockets.
type ManagedRealtimeDrainOperation struct {
	ID         string
	AccountID  string
	AppID      string
	EndpointID string
	Status     ManagedRealtimeDrainOperationStatus
	Reason     string
	DryRun     bool
	Matched    int
	Closed     int
	Gone       int
	Failed     int
	Result     json.RawMessage
	// ConnectionIDs contains the still-pending selection while an operation
	// is running. Terminal per-connection results are kept in Result so a
	// retry only needs to revisit transient owner failures.
	ConnectionIDs    []string
	Limit            int
	All              bool
	Truncated        bool
	Partial          bool
	NodesQueried     int
	NodesUnavailable int
	Attempts         int
	NextAttemptAt    time.Time
	ClaimedAt        *time.Time
	ClaimToken       string
	LastError        string
	CreatedAt        time.Time
	CompletedAt      *time.Time
}

type ManagedRealtimeDrainOperationClaim struct {
	Operation  ManagedRealtimeDrainOperation
	ClaimToken string
}

type ManagedRealtimeDrainOperationInput struct {
	AccountID        string
	AppID            string
	EndpointID       string
	Reason           string
	DryRun           bool
	Matched          int
	ConnectionIDs    []string
	Limit            int
	All              bool
	Truncated        bool
	Partial          bool
	NodesQueried     int
	NodesUnavailable int
}
