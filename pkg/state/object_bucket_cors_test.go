// adr: 20261025202805

package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectBucketCORSMem(t *testing.T) { objectBucketCORSSuite(t, state.NewMemStore()) }
func TestObjectBucketCORSPG(t *testing.T) {
	st, _ := pgStore(t)
	objectBucketCORSSuite(t, st)
}

func objectBucketCORSSuite(t *testing.T, st accountingStore) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	cors := st.(state.ObjectBucketCORSStore)
	reader := st.(state.ObjectBucketCORSReader)
	empty, err := cors.GetObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || empty.Revision != 0 || len(empty.Rules) != 0 || empty.Rules == nil {
		t.Fatalf("unset configuration = %+v, %v", empty, err)
	}
	rules := []api.ObjectBucketCORSRule{{ID: "uploads", AllowedOrigins: []string{"https://app.example.test"}, AllowedMethods: []string{"PUT"}, AllowedHeaders: []string{"content-type"}, MaxAgeSeconds: 60}}
	first, err := cors.PutObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID, rules)
	if err != nil || first.Revision != 1 || len(first.Rules) != 1 {
		t.Fatalf("first put = %+v, %v", first, err)
	}
	rules = append(rules, api.ObjectBucketCORSRule{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}})
	second, err := cors.PutObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID, rules)
	if err != nil || second.Revision != 2 || len(second.Rules) != 2 {
		t.Fatalf("replacing put = %+v, %v", second, err)
	}
	got, err := cors.GetObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || got.Revision != 2 || got.Rules[0].ID != "uploads" || got.Rules[1].AllowedOrigins[0] != "*" {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	if served, err := reader.ObjectBucketCORSRules(ctx, b.ID); err != nil || len(served) != 2 {
		t.Fatalf("gateway rules = %+v, %v", served, err)
	}
	if _, err = cors.PutObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectBucketCORSRule{{AllowedOrigins: []string{"https://app.example.test"}}}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("invalid rules stored", err)
	}
	if _, err = cors.GetObjectBucketCORS(ctx, uuid.NewString(), b.AppID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("another account read the rules", err)
	}
	if err = cors.DeleteObjectBucketCORS(ctx, uuid.NewString(), b.AppID, b.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("another account deleted the rules", err)
	}
	if err = cors.DeleteObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err = cors.DeleteObjectBucketCORS(ctx, b.AccountID, b.AppID, b.ID); err != nil {
		t.Fatal("deleting absent rules", err)
	}
	if served, err := reader.ObjectBucketCORSRules(ctx, b.ID); err != nil || served != nil {
		t.Fatalf("deleted rules still served = %+v, %v", served, err)
	}
}
