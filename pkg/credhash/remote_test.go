package credhash_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// adr: 20261029202403 amendment 1 decision 2 — a daemon holding no ring for
// a family asks the remote installed for it, and a remote failure refuses.
func TestMatchAsksTheRemoteForAFamilyWithoutARing(t *testing.T) {
	const family = credhash.FamilyDataAPI
	const tag credhash.Tag = "data_api_access_configs.server_key"
	t.Cleanup(func() { credhash.SetRemote(family, nil); credhash.Set(family, nil) })
	sum := []byte(strings.Repeat("s", 32))
	if !credhash.Match(context.Background(), family, tag, sum, sum) {
		t.Fatal("without a ring or a remote a plain hash must match locally")
	}
	asked := 0
	answer, fail := true, error(nil)
	credhash.SetRemote(family, func(_ context.Context, gotTag credhash.Tag, stored, gotSum []byte) (bool, error) {
		asked++
		if gotTag != tag || string(gotSum) != string(sum) || string(stored) != "stored" {
			t.Fatalf("remote asked about %s %q %q", gotTag, gotSum, stored)
		}
		return answer, fail
	})
	if !credhash.Match(context.Background(), family, tag, sum, []byte("stored")) || asked != 1 {
		t.Fatal("the remote's yes was not used")
	}
	answer = false
	if credhash.Match(context.Background(), family, tag, sum, []byte("stored")) {
		t.Fatal("the remote's no was not used")
	}
	answer, fail = true, errors.New("apid unreachable")
	if credhash.Match(context.Background(), family, tag, sum, []byte("stored")) {
		t.Fatal("a remote failure accepted the credential")
	}
	if credhash.AnyKeyedWrites() {
		t.Fatal("no policy installed, yet keyed writes reported")
	}
	credhash.Set(family, &credhash.Policy{Ring: ring(t, 1), KeyedWrites: true, PlainLookup: true})
	if !credhash.AnyKeyedWrites() {
		t.Fatal("an installed keyed policy was not reported")
	}
}
