package state_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type multipartURLTestStore interface {
	objectURLTestStore
	state.ObjectMultipartURLCapabilityStore
	state.ObjectMultipartUploadStore
	state.ObjectMultipartTransferStore
}

// adr: 558
func TestObjectMultipartURLCapabilitiesMem(t *testing.T) {
	multipartURLCapabilitySuite(t, state.NewMemStore(), nil)
}
func TestObjectMultipartURLCapabilitiesPG(t *testing.T) {
	st, pool, _ := pgStoreWithPool(t)
	multipartURLCapabilitySuite(t, st, pool)
}

func multipartURLCapabilitySuite(t *testing.T, st multipartURLTestStore, pool *pgxpool.Pool) {
	ctx := t.Context()
	b, _ := seedAccounting(t, st)
	p := accountingPolicy()
	if err := st.AdmitObjectURL(ctx, b.AccountID, b.ID, "fixed", 10, true, p); err != nil {
		t.Fatal(err)
	}
	u, err := st.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Key: "fixed", SizeBytes: 10, PartSizeBytes: 10, PartCount: 1, ExpiresAt: time.Now().Add(time.Hour)}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = st.ActivateObjectMultipartUpload(ctx, u.ID, "init", "private-native-upload"); err != nil {
		t.Fatal(err)
	}
	u, err = st.GetObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, _ := api.GenerateAPIKey()
	k, err := st.CreateAPIKey(ctx, b.AccountID, hash, "part issuer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetObjectBucketAccessGrant(ctx, b.AccountID, b.ID, k.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	size := int64(10)
	c := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: b.AccountID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "recipient", Label: "part URL", Permission: state.ObjectBucketPermissionWrite, Status: state.ObjectS3CredentialStatusActive}
	c.URL = &state.ObjectURLCapability{Request: api.ObjectSignRequest{Method: "PUT", Key: u.Key, SizeBytes: &size, ExpiresIn: 300, ContentType: "application/octet-stream"}, APIKeyID: k.ID, ExpiresAt: time.Now().Add(4 * time.Minute), Multipart: &state.ObjectURLMultipartPart{UploadID: u.ID, PartNumber: 1}}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := st.IssueObjectMultipartURLCredential(ctx, c, u, p)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate part URL admission", wins.Load())
	}
	resolved, _, err := st.ResolveObjectS3Credential(ctx, access)
	if err != nil || resolved.URL.Multipart.UploadID != u.ID {
		t.Fatal("part descriptor lost", resolved, err)
	}
	resolved.URL.Multipart.UploadID = uuid.NewString()
	resolved, _, err = st.ResolveObjectS3Credential(ctx, access)
	if err != nil || resolved.URL.Multipart.UploadID != u.ID {
		t.Fatal("part descriptor aliases stored authority", err)
	}
	if _, _, err = st.IssueObjectURLCredential(ctx, c, state.ObjectUploadCompletion{}, p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("part URL used single-write admission", err)
	}
	if err = st.DeleteObjectBucketAccessGrant(ctx, b.AccountID, b.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if err = st.BeginObjectURLMultipartPart(ctx, c.ID, "revoked", p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("revoked issuer dispatched", err)
	}
	if _, err = st.SetObjectBucketAccessGrant(ctx, b.AccountID, b.ID, k.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	wins.Store(0)
	winner := make(chan string, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token := uuid.NewString()
			e := st.BeginObjectURLMultipartPart(ctx, c.ID, token, p)
			if e == nil {
				wins.Add(1)
				winner <- token
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("overlapping native part attempts", wins.Load())
	}
	token := <-winner
	if pool != nil {
		assertMultipartURLDatabaseFences(t, pool, u, c)
	}
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}
	if _, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "complete", state.ObjectMultipartCompleting, parts, false); !errors.Is(err, state.ErrConflict) {
		t.Fatal("completed during part transfer", err)
	}
	usage, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 10 || usage.Buckets[0].MultipartBytes != 0 {
		t.Fatal("part duplicated the full-object reservation", usage, err)
	}
	if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "wrong"); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign attempt settled", err)
	}
	if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, token); err != nil {
		t.Fatal(err)
	}
	if err = st.BeginObjectURLMultipartPart(ctx, c.ID, "replacement", p); err != nil {
		t.Fatal("settled part could not be replaced", err)
	}
	if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, token); !errors.Is(err, state.ErrConflict) {
		t.Fatal("old attempt settled its replacement", err)
	}
	if _, err = st.ClaimObjectMultipartUpload(ctx, b.AccountID, b.AppID, b.ID, u.ID, "abort", state.ObjectMultipartAborting, nil, false); err != nil {
		t.Fatal(err)
	}
	if err = st.BeginObjectURLMultipartPart(ctx, c.ID, "late", p); !errors.Is(err, state.ErrConflict) {
		t.Fatal("part URL outlived its session cutoff", err)
	}
	if ready, e := st.ObjectMultipartAbortReady(ctx, u.ID, "abort"); e != nil || ready {
		t.Fatal("abort ignored an unsafe part", ready, e)
	}
	if err = st.DeleteAPIKey(ctx, b.AccountID, k.ID); err != nil {
		t.Fatal(err)
	}
	if err = st.SettleObjectMultipartPart(ctx, b.AccountID, u.ID, 1, "replacement"); err != nil {
		t.Fatal("inflight settlement failed after revocation", err)
	}
	if ready, e := st.ObjectMultipartAbortReady(ctx, u.ID, "abort"); e != nil || !ready {
		t.Fatal("unused URL delayed drained abort", ready, e)
	}
	metrics, err := st.(state.ObjectStorageProviderUsageStore).ListObjectStorageProviderRequestMetrics(ctx, b.BackendID, b.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != 2 {
		t.Fatal("losing native attempts were metered", metrics, err)
	}
}

func assertMultipartURLDatabaseFences(t *testing.T, pool *pgxpool.Pool, u state.ObjectMultipartUpload, c state.ObjectS3Credential) {
	t.Helper()
	for _, q := range []string{
		`UPDATE object_storage_multipart_uploads SET state='completing',completion_parts='[{"part_number":1,"etag":"part"}]'::jsonb WHERE id=$1`,
		`UPDATE object_storage_multipart_part_grants SET url_credential_id='11111111-1111-4111-8111-111111111111' WHERE upload_id=$1`,
		`INSERT INTO object_storage_multipart_part_grants(upload_id,part_number,max_bytes) VALUES($1,1,10)`,
	} {
		_, err := pool.Exec(t.Context(), q, u.ID)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.ConstraintName != "object_url_capability_fenced" {
			t.Fatal("older fixed multipart writer bypassed its dispatch fence", err)
		}
	}
	_, err := pool.Exec(t.Context(), `UPDATE object_storage_s3_credentials SET url_request=jsonb_set(url_request,'{multipart,part_number}','2') WHERE id=$1`, c.ID)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.ConstraintName != "object_url_capability_fenced" {
		t.Fatal("part URL descriptor was mutable", err)
	}
	body, err := migrations.FS.ReadFile("20261004090600504_object_multipart_url_capabilities.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(body), "-- +goose Down")
	if !ok {
		t.Fatal("rollback section missing")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context()) //nolint:errcheck
	_, err = tx.Exec(t.Context(), down)
	if err == nil || !strings.Contains(err.Error(), "Drain multipart URL capabilities") {
		t.Fatal("rollback did not preserve live multipart authority", err)
	}
}
