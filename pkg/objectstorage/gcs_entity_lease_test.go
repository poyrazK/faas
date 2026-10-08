// adr: 712
package objectstorage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type gcsLeaseStore struct {
	durableentity.ObjectStore
	initial time.Time
	ack     chan time.Time
	lose    bool
	writes  atomic.Int64
}

func (s *gcsLeaseStore) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	var value struct {
		Version uint64    `json:"state_version"`
		Owner   string    `json:"owner_id"`
		Expires time.Time `json:"expires_at"`
	}
	renewal := strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.Version == 0 && value.Owner != "" && value.Expires.After(s.initial.Add(api.DurableEntityInvocationLease))
	next, err := s.ObjectStore.Put(ctx, key, body, version)
	if renewal {
		s.writes.Add(1)
		if err == nil && s.lose {
			return "", errors.New("fixture lost acknowledged GCS renewal")
		}
		if err == nil {
			select {
			case s.ack <- value.Expires:
			default:
			}
		}
	}
	return next, err
}

func TestGCSWireManagedInvocationRenewalAndLostAcknowledgement(t *testing.T) {
	for _, lose := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "lost acknowledgement"}[lose], func(t *testing.T) {
			_, provider := newGCSEntityWire(t)
			native, err := durableentity.NewProviderStore(provider, "private")
			if err != nil {
				t.Fatal(err)
			}
			initial, clock := time.Now().UTC(), &atomic.Int64{}
			clock.Store(initial.UnixNano())
			store := &gcsLeaseStore{ObjectStore: native, initial: initial, ack: make(chan time.Time, 8), lose: lose}
			m, err := durableentity.Open(t.Context(), store, durableentity.Options{InvocationLeaseDuration: api.DurableEntityInvocationLease, InvocationRenewInterval: 10 * time.Millisecond, Now: func() time.Time { return time.Unix(0, clock.Load()) }})
			if err != nil {
				t.Fatal(err)
			}
			qualifyGCSManagedLease(t, m, store, clock, lose)
		})
	}
}

func qualifyGCSManagedLease(t *testing.T, m *durableentity.Manager, store *gcsLeaseStore, clock *atomic.Int64, lose bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	id := durableentity.ID{AccountID: "account", AppID: "app", Namespace: "counters", Key: "managed-lease"}
	result, err := m.Invoke(ctx, id, "owner", gcsEntityRequest("one"), func(ctx context.Context, view durableentity.View) (durableentity.Transition, error) {
		for range 2 {
			clock.Add(int64(20 * time.Second))
			wanted := time.Unix(0, clock.Load()).Add(api.DurableEntityInvocationLease)
			for {
				select {
				case <-ctx.Done():
					// A late valid response cannot publish after a lost renewal ACK.
					return gcsEntityIncrement(ctx, view)
				case expiry := <-store.ack:
					if expiry.Before(wanted) {
						continue
					}
				}
				break
			}
		}
		return gcsEntityIncrement(ctx, view)
	})
	if lose {
		if !errors.Is(err, durableentity.ErrUncertain) || result.Version != 0 || store.writes.Load() != 1 {
			t.Fatal("uncertain native renewal was retried or published state", result, err, store.writes.Load())
		}
		store.lose = false // Invoke has joined its renewal writer before returning.
		result, err = m.Invoke(ctx, id, "replacement", gcsEntityRequest("one"), gcsEntityIncrement)
	}
	if err != nil || result.Version != 1 || string(result.Value) != `{"count":1}` || !lose && store.writes.Load() < 2 {
		t.Fatal("GCS renewal changed the committed result", result, err)
	}
	replay, err := m.Invoke(ctx, id, "retry", gcsEntityRequest("one"), gcsEntityIncrement)
	if err != nil || !replay.Replayed || replay.Version != result.Version || string(replay.Value) != string(result.Value) {
		t.Fatal("GCS renewal lost original receipt replay", replay, err)
	}
}
