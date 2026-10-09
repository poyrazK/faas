// adr: 851
package durableentity

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestInvokeRestoreNeverCreatesMissingOrVersionZeroState(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{f.id, {AccountID: "account-a", AppID: "app-a", Namespace: "customers", Key: "absent"}} {
		exported := StateExport{Format: 1, Entity: id, Version: 1, Data: json.RawMessage(`{}`)}
		exported.Checksum = exportChecksum(exported)
		f.store.mu.Lock()
		before := len(f.store.objects)
		f.store.mu.Unlock()
		if _, err := f.manager.InvokeRestoreState(t.Context(), id, "worker", "restore", 1, exported); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
		f.store.mu.Lock()
		after := len(f.store.objects)
		f.store.mu.Unlock()
		if before != after {
			t.Fatal("restore created an object")
		}
	}
}
