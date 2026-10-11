package apidserver

import (
	"github.com/onebox-faas/faas/pkg/state"
)

// memSeedInvoice injects a row directly into the MemStore's invoice map.
// PR A does not yet expose an UpsertInvoice seam — that lands in PR B
// alongside webhook ingestion. Until then, tests that need to assert
// listInvoices behaviour bypass the writer and reach the map via
// state.MemStore's package-internal hook below. The pgstore tests use
// the parallel seedInvoiceFixture helper against the live DB.
func memSeedInvoice(store *state.MemStore, inv state.Invoice) {
	store.SeedInvoiceForTest(inv)
}
