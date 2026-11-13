// adr: 20261106214916

package main

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// snapshotRefreshKeys are the runtime-config keys schedd's snapshot
// refresher applies (ADR-20261106214916).
var snapshotRefreshKeys = []string{
	runtimeconfig.KeySnapshotRefresh,
	runtimeconfig.KeySnapshotRefreshWindowHours,
}

// snapshotRefreshTarget is the part of the refresher the keys set.
type snapshotRefreshTarget interface {
	SetMode(string) error
	SetWindowHours(int) error
}

// applySnapshotRefreshConfig installs one key's value. handled is false for a
// key it does not own. A value outside its bounds fails the apply and the
// refresher keeps the last value it applied.
func applySnapshotRefreshConfig(r snapshotRefreshTarget, key string, value json.RawMessage) (handled bool, err error) {
	switch key {
	case runtimeconfig.KeySnapshotRefresh:
		mode, err := runtimeconfig.String(value)
		if err != nil {
			return true, fmt.Errorf("%s must be a string: %w", key, err)
		}
		return true, r.SetMode(mode)
	case runtimeconfig.KeySnapshotRefreshWindowHours:
		hours, err := runtimeconfig.Int(value)
		if err != nil {
			return true, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return true, r.SetWindowHours(hours)
	}
	return false, nil
}
