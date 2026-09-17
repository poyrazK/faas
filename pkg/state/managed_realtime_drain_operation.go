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
// bounded connection drain. Result contains the complete API response so the
// operation can be inspected after the live owner has forgotten the sockets.
type ManagedRealtimeDrainOperation struct {
	ID          string
	AccountID   string
	AppID       string
	EndpointID  string
	Status      ManagedRealtimeDrainOperationStatus
	Reason      string
	DryRun      bool
	Matched     int
	Closed      int
	Gone        int
	Failed      int
	Result      json.RawMessage
	CreatedAt   time.Time
	CompletedAt *time.Time
}

type ManagedRealtimeDrainOperationInput struct {
	AccountID  string
	AppID      string
	EndpointID string
	Reason     string
	DryRun     bool
	Matched    int
}
