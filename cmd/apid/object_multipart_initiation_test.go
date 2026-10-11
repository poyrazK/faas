//go:build !no_pg

// adr: 590
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type initiationAPIProvider struct {
	*fakeObjectProvider
	writes int
	lost   bool
}

func (p *initiationAPIProvider) InitiateMultipartUpload(ctx context.Context, _ string, _ objectstorage.MultipartCreateRequest, _ objectstorage.ResolvedObjectEncryption, dispatch func(context.Context) error) (string, error) {
	if err := dispatch(ctx); err != nil {
		return "", err
	}
	p.writes++
	if p.lost {
		return "", objectstorage.ErrUnavailable
	}
	return "native-original", nil
}

func TestObjectMultipartInitiationAPIRecoveryMem(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive", true: "unknown"}[lost], func(t *testing.T) {
			e := setup(t, api.PlanPro)
			now := time.Now()
			e.store.SetClockForTest(func() time.Time { return now })
			multipartInitiationAPIRecovery(t, e.h, e.s, e.store, e.key, e.acct, lost, func(string) { now = now.Add(state.ObjectMultipartLeaseDuration + time.Second) })
		})
	}
}

func TestObjectMultipartInitiationAPIRecoveryPG(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "positive", true: "unknown"}[lost], func(t *testing.T) {
			e := setupPGHandler(t, api.PlanPro)
			multipartInitiationAPIRecovery(t, e.h, e.s, e.store, e.key, e.acct, lost, func(id string) { expireInitiationAPIFixture(t, e.pool, id) })
		})
	}
}

func expireInitiationAPIFixture(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE object_storage_multipart_uploads SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func multipartInitiationAPIRecovery(t *testing.T, h http.Handler, s *server, st state.Store, key string, account state.Account, lost bool, expire func(string)) {
	t.Helper()
	ctx := t.Context()
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "initiation", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	p := &initiationAPIProvider{fakeObjectProvider: &fakeObjectProvider{}, lost: lost}
	s.WithObjectStorage(objectRegistry(t, p, &fakeObjectProvider{}, "external"))
	local := httptest.NewServer(h)
	defer local.Close()
	c := api.NewClient(local.URL, key)
	public, err := c.CreateObjectBucket(ctx, app.Slug, api.CreateObjectBucketRequest{Name: "assets"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.(state.ObjectBucketStore).GetObjectBucket(ctx, account.ID, app.ID, public.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "original", ExpiresAt: time.Now().Add(time.Hour)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "original-owner", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ensureAdmittedObjectMultipart(ctx, sessions, p, b, u, objectstorage.MultipartCreateRequest{SessionID: u.ID, Key: u.Key})
	if lost && !errors.Is(err, objectstorage.ErrUnavailable) || !lost && err != nil {
		t.Fatal(err)
	}
	// Simulate stopping after provider observation but before activation.
	expire(u.ID)
	fences := st.(state.ObjectBucketWriteFenceStore)
	if _, err := fences.BeginObjectBucketMutation(ctx, b, state.ObjectBucketMutationRequest); err != nil {
		t.Fatal(err)
	}
	f, err := fences.AcquireObjectBucketWriteFence(ctx, b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "replacement-owner", state.ObjectMultipartInitiating, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.executeObjectMultipartOperation(ctx, sessions, b, u)
	if lost && !errors.Is(err, objectstorage.ErrUnavailable) || !lost && err != nil || p.writes != 1 {
		t.Fatal("recovery wrote another upload", err, p.writes)
	}
	f, err = fences.ReadObjectBucketWriteFence(ctx, b, f.Token)
	if err != nil || f.Requests != 2 || f.Multipart != 1 {
		t.Fatal("activation or uncertainty drained original evidence", f, err)
	}
}
