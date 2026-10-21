package imaged

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// H10-95: the rolling pre-stage runs the candidate imaged against the
// previous release's vmmd. Only that pre-stage may leave a child base whose
// verified parent RPC is missing for the serving imaged; nothing else defers.
func TestReconcileAssignedRuntimesDefersOnlyLegacyParentInPrestage(t *testing.T) {
	legacy := fmt.Errorf("imaged: stage runtime base node24: %w", fmt.Errorf("%w: rpc error: code = Unimplemented", ErrVerifiedParentUnsupported))
	other := errors.New("imaged: verified parent materialization: rpc error: code = Unavailable")
	tests := []struct {
		name         string
		defer_       bool
		fail         map[string]error
		wantDeferred []string
		wantStaged   []string
		wantErr      bool
	}{
		{name: "prestage defers legacy parent and keeps going", defer_: true, fail: map[string]error{"node24": legacy, "python312": legacy}, wantDeferred: []string{"node24", "python312"}, wantStaged: []string{"go124", "node22", "node24", "python312", "python313"}},
		{name: "serving imaged keeps legacy parent fatal", fail: map[string]error{"node24": legacy}, wantStaged: []string{"go124", "node22", "node24"}, wantErr: true},
		{name: "prestage keeps other failures fatal", defer_: true, fail: map[string]error{"node24": other}, wantStaged: []string{"go124", "node22", "node24"}, wantErr: true},
		{name: "nothing to defer", defer_: true, wantStaged: []string{"go124", "node22", "node24", "python312", "python313"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var staged []string
			deferred, err := reconcileAssignedRuntimes([]string{"go124", "node22", "node24", "python312", "python313"}, tc.defer_, func(runtime string) error {
				staged = append(staged, runtime)
				return tc.fail[runtime]
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(deferred, tc.wantDeferred) {
				t.Fatalf("deferred = %v, want %v", deferred, tc.wantDeferred)
			}
			if !reflect.DeepEqual(staged, tc.wantStaged) {
				t.Fatalf("staged = %v, want %v", staged, tc.wantStaged)
			}
		})
	}
}
