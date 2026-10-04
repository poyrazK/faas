package state

import (
	"context"
	"time"
)

// Selectors come only from permission-filtered binding catalogs. The store
// reads managed secret versions and receipts, never ciphertext or value hashes.
type BindingAdoptionSelector struct {
	Type, BindingID, Scope string
	Keys                   []string
}

// A row with no InstanceID records an existing managed secret without an
// authorized resident consumer. Missing secrets produce no row.
type BindingApplicationAdoptionRow struct {
	Type, BindingID, Scope, Key                 string
	CurrentVersion                              int64
	DeploymentID, InstanceID, WorkloadName      string
	RuntimeState, ReloadSupport                 string
	ReloadVersion                               int64
	Projection, Signal                          string
	ReloadAt                                    *time.Time
	ApplicationAckVersion                       int64
	ApplicationAck                              string
	ApplicationAckAt                            *time.Time
	ProcessGeneration, ApplicationAckGeneration string
}

type BindingApplicationAdoptionStore interface {
	ReadBindingApplicationAdoption(context.Context, string, string, []BindingAdoptionSelector) ([]BindingApplicationAdoptionRow, error)
}
