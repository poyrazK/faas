package productemail_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/credhash"
	"github.com/onebox-faas/faas/pkg/productemail"
)

// adr: 20261029202403 — an unsubscribe link minted plain keeps working
// through keyed writes, one minted keyed is stored as a digest, and once
// plain lookup is off a plain row no longer unsubscribes anyone.
func TestProductEmailUnsubscribeKeyedHashes(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	keyed := &credhash.Policy{Ring: ring, KeyedWrites: true, PlainLookup: true}
	contract := &credhash.Policy{Ring: ring, KeyedWrites: true}
	t.Cleanup(func() { credhash.SetAccount(nil) })
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "postgres"}[pg], func(t *testing.T) {
			repo, k := ownerRepository(t, pg)
			now := time.Now().UTC().Truncate(time.Microsecond)

			credhash.SetAccount(nil)
			sub := ownerSubscription(t, repo, k, now)
			_, plain := preparedOptOut(t, repo, sub, now)
			credhash.SetAccount(contract)
			if _, err := repo.Unsubscribe(t.Context(), plain, now.Add(time.Second)); !errors.Is(err, productemail.ErrNotFound) {
				t.Fatalf("contract: a plain link unsubscribed: %v", err)
			}
			credhash.SetAccount(keyed)
			if v, err := repo.Unsubscribe(t.Context(), plain, now.Add(time.Second)); err != nil || !v.Current {
				t.Fatalf("keyed writes: plain link = %+v, %v", v, err)
			}

			sub, err = repo.Get(t.Context(), k)
			if err != nil {
				t.Fatal(err)
			}
			sub.Work, sub.Help, sub.Grant, sub.ExpiresAt = true, true, []byte("fresh-encrypted-consent"), now.Add(time.Hour)
			if sub, err = repo.Put(t.Context(), sub, sub.Version, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			_, sum := preparedOptOut(t, repo, sub, now.Add(3*time.Second))
			credhash.SetAccount(contract)
			if v, err := repo.Unsubscribe(t.Context(), sum, now.Add(4*time.Second)); err != nil || !v.Current {
				t.Fatalf("contract: keyed link = %+v, %v", v, err)
			}
		})
	}
}
