package liveproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 decision 2 — realtimed holds no data-api
// ring: a keyed server key hash is matched by asking apid, and an apid
// failure refuses the key.
func TestMatchServerKeyAsksApidForKeyedHashes(t *testing.T) {
	line, err := credhash.NewKeyLine(1)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := credhash.ParseRing([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	apid := &credhash.Policy{Ring: ring, KeyedWrites: true}
	t.Cleanup(func() { credhash.SetRemote(credhash.FamilyDataAPI, nil) })

	sumA := sha256.Sum256([]byte("gdsk_a"))
	sumB := sha256.Sum256([]byte("gdsk_b"))
	keys := []api.DataAPIServerKey{
		{Slot: "a", SHA256: hex.EncodeToString(apid.Store(api.DataAPIServerKeyCredentialTag, sumA[:]))},
		{Slot: "b", SHA256: hex.EncodeToString(sumB[:])},
	}
	if _, ok := matchServerKey(context.Background(), keys, "gdsk_a"); ok {
		t.Fatal("a keyed hash matched without the ring or apid")
	}
	if k, ok := matchServerKey(context.Background(), keys, "gdsk_b"); !ok || k.Slot != "b" {
		t.Fatal("a plain hash no longer matched locally")
	}
	credhash.SetRemote(credhash.FamilyDataAPI, func(_ context.Context, tag credhash.Tag, stored, sum []byte) (bool, error) {
		return apid.Matches(tag, sum, stored), nil
	})
	if k, ok := matchServerKey(context.Background(), keys, "gdsk_a"); !ok || k.Slot != "a" {
		t.Fatal("apid's match was not used")
	}
	if _, ok := matchServerKey(context.Background(), keys, "gdsk_other"); ok {
		t.Fatal("an unknown key matched")
	}
	credhash.SetRemote(credhash.FamilyDataAPI, func(context.Context, credhash.Tag, []byte, []byte) (bool, error) {
		return false, errors.New("apid unreachable")
	})
	if _, ok := matchServerKey(context.Background(), keys, "gdsk_a"); ok {
		t.Fatal("an apid failure accepted the key")
	}
}
