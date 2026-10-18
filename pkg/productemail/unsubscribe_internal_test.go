package productemail

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductEmailUnsubscribeAtVersionCeiling(t *testing.T) {
	now := time.Now().UTC()
	k := Key{AccountID: uuid.NewString(), AppID: uuid.NewString(), TenantID: uuid.NewString(), AuthAppID: uuid.NewString(), OrganizationID: uuid.NewString(), ActorID: uuid.NewString()}
	m := NewMemory()
	s := Subscription{Key: k, AppSlug: "exports", AuthSlug: "auth", Version: api.ProductControlNotificationMaxUpdates, Generation: 1, Work: true, Help: true, Grant: []byte("encrypted"), ExpiresAt: now.Add(time.Hour), PauseReason: "complaint", PausedAt: now}
	token, err := NewUnsubscribeToken()
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := UnsubscribeHash(token)
	d := Delivery{ProductEmailDelivery: api.ProductEmailDelivery{ID: uuid.NewString(), Status: "provider_accepted", CreatedAt: now}, SubscriptionID: k.ID(), Version: s.Version, Generation: s.Generation, UnsubscribeHash: hash}
	m.subscriptions[k.ID()], m.deliveries[d.ID] = s, d
	v, err := m.Unsubscribe(t.Context(), hash, now)
	if err != nil || !v.Current {
		t.Fatal(v, err)
	}
	current, _ := m.Get(t.Context(), k)
	if current.Work || current.Help || len(current.Grant) != 0 || current.Version != s.Version || current.PauseReason != "complaint" {
		t.Fatal("preference ceiling prevented opt-out or cleared hold", current)
	}
}
