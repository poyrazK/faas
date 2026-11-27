package runfile_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/runfile"
)

func TestOwnerScopeAcceptsBothStoreFormatsWithoutAliasing(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	compact := "12345678123442348234123456789abc"
	for _, account := range []string{id, compact} {
		key := runfile.Key(account, id, id)
		if err := runfile.ValidateKey(account, id, key); err != nil {
			t.Fatal(err)
		}
		other := id
		if account == id {
			other = compact
		}
		if err := runfile.ValidateKey(other, id, key); err == nil {
			t.Fatal("owner format alias crossed a storage scope")
		}
	}
	for _, owner := range []string{"../account", "00000000000000000000000000000000", "12345678123442348234123456789ABC"} {
		if runfile.ValidOwnerID(owner) {
			t.Fatal("unsafe owner accepted")
		}
	}
}
