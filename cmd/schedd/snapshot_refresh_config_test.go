// adr: 20261106214916

package main

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestApplySnapshotRefreshConfig(t *testing.T) {
	r := sched.NewSnapshotRefresher(nil, nil, nil)
	for _, tc := range []struct {
		key, value     string
		handled, apply bool
	}{
		{runtimeconfig.KeySnapshotRefresh, `"shadow"`, true, true},
		{runtimeconfig.KeySnapshotRefresh, `"off"`, true, true},
		{runtimeconfig.KeySnapshotRefresh, `"on"`, true, true},
		{runtimeconfig.KeySnapshotRefresh, `"yes"`, true, false},
		{runtimeconfig.KeySnapshotRefresh, `1`, true, false},
		{runtimeconfig.KeySnapshotRefreshWindowHours, `48`, true, true},
		{runtimeconfig.KeySnapshotRefreshWindowHours, `0`, true, false},
		{runtimeconfig.KeySnapshotRefreshWindowHours, `"48"`, true, false},
		{runtimeconfig.KeyDataPlacement, `true`, false, true},
	} {
		handled, err := applySnapshotRefreshConfig(r, tc.key, json.RawMessage(tc.value))
		if handled != tc.handled || (err == nil) != tc.apply {
			t.Errorf("apply(%s, %s) = handled %v, err %v; want handled %v, applied %v", tc.key, tc.value, handled, err, tc.handled, tc.apply)
		}
	}
	for _, key := range snapshotRefreshKeys {
		if key != runtimeconfig.KeySnapshotRefresh && key != runtimeconfig.KeySnapshotRefreshWindowHours {
			t.Errorf("unexpected key %s", key)
		}
	}
}
