package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 627
func TestConfigurationRequestBudgets(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "memory"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			var st multipartCopyIntegrationStore = state.NewMemStore()
			if pg {
				st, _ = multipartCopyPGStore(t)
			}
			configurationRequestBudgetSuite(t, st)
		})
	}
}

func configurationRequestBudgetSuite(t *testing.T, st multipartCopyIntegrationStore) {
	t.Helper()
	var calls atomic.Int32
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}), objectstorage.Config{}, 0)
	configureRequestBudget(t, f, 1)
	if err := st.(state.ObjectStorageGatewayRequestStore).ReserveObjectStorageGatewayRequest(t.Context(), f.bucket.ID, time.Now(), f.handler.registry.Accounting); err != nil {
		t.Fatal(err)
	}
	bucket, key, version := aws.String("assets"), aws.String("key"), aws.String("null")
	for _, tc := range []struct {
		name string
		call func(*awss3.Client) error
	}{
		{"get versioning", func(c *awss3.Client) error {
			_, err := c.GetBucketVersioning(t.Context(), &awss3.GetBucketVersioningInput{Bucket: bucket})
			return err
		}},
		{"get encryption", func(c *awss3.Client) error {
			_, err := c.GetBucketEncryption(t.Context(), &awss3.GetBucketEncryptionInput{Bucket: bucket})
			return err
		}},
		{"get object lock", func(c *awss3.Client) error {
			_, err := c.GetObjectLockConfiguration(t.Context(), &awss3.GetObjectLockConfigurationInput{Bucket: bucket})
			return err
		}},
		{"get retention", func(c *awss3.Client) error {
			_, err := c.GetObjectRetention(t.Context(), &awss3.GetObjectRetentionInput{Bucket: bucket, Key: key, VersionId: version})
			return err
		}},
		{"get legal hold", func(c *awss3.Client) error {
			_, err := c.GetObjectLegalHold(t.Context(), &awss3.GetObjectLegalHoldInput{Bucket: bucket, Key: key, VersionId: version})
			return err
		}},
		{"put versioning", func(c *awss3.Client) error {
			_, err := c.PutBucketVersioning(t.Context(), &awss3.PutBucketVersioningInput{Bucket: bucket, VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}})
			return err
		}},
		{"put encryption", func(c *awss3.Client) error { return putConfigurationAES256(t, c) }},
		{"put object lock", func(c *awss3.Client) error {
			_, err := c.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: bucket, ObjectLockConfiguration: &types.ObjectLockConfiguration{ObjectLockEnabled: types.ObjectLockEnabledEnabled}})
			return err
		}},
		{"put retention", func(c *awss3.Client) error {
			_, err := c.PutObjectRetention(t.Context(), &awss3.PutObjectRetentionInput{Bucket: bucket, Key: key, VersionId: version, Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeGovernance, RetainUntilDate: aws.Time(time.Now().Add(24 * time.Hour))}})
			return err
		}},
		{"put legal hold", func(c *awss3.Client) error {
			_, err := c.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: bucket, Key: key, VersionId: version, LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 2 {
				assertConfigurationBudgetError(t, tc.call(f.client))
			}
			if calls.Load() != 0 {
				t.Fatal("exhausted configuration request reached native provider", calls.Load())
			}
			if tc.name == "put encryption" {
				j, err := st.(state.ObjectBucketEncryptionStore).GetObjectBucketEncryption(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
				if err != nil || j.Revision != 0 || j.State != "ready" {
					t.Fatal("exhausted budget accepted a new encryption intent", j, err)
				}
			}
		})
	}
}

func configureRequestBudget(t *testing.T, f *multipartCopyIntegration, limit int64) {
	t.Helper()
	policy := f.handler.registry.Accounting
	since := time.Now().Add(-time.Hour)
	policy.AccountingMode, policy.GatewayMeteringSince = api.ObjectStorageGatewaySafetyV1, &since
	policy.MaxMonthlyCostMillicents, policy.MaxMonthlyRequests = 0, limit
	r, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, PublicEndpoint: "https://s3.gregale.dev", Transfer: objectstorage.ObjectTransferConfig{Profile: "proxied"}, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "local"}, Backends: []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "integration", Endpoint: f.nativeEndpoint, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", ObjectLock: objectstorage.ObjectLockConfig{Enabled: true}, Encryption: objectstorage.EncryptionConfig{Algorithms: []string{"AES256"}}}}}, func(string) string { return "local-provider-test-credential" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	f.handler.registry = r
}

func assertConfigurationBudgetError(t *testing.T, err error) {
	t.Helper()
	assertSDKErrorCode(t, err, "AccountProblem")
	var response *smithyhttp.ResponseError
	if !errors.As(err, &response) || response.HTTPStatusCode() != http.StatusPaymentRequired {
		t.Fatal("budget error lost its HTTP status", err)
	}
}

func configurationLastRequest(t *testing.T, st multipartCopyIntegrationStore) {
	t.Helper()
	t.Run("concurrent last request", func(t *testing.T) {
		var calls atomic.Int32
		f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
		}), objectstorage.Config{}, 0)
		configureRequestBudget(t, f, 1)
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				_, err := f.client.GetBucketVersioning(t.Context(), &awss3.GetBucketVersioningInput{Bucket: aws.String("assets")})
				results <- err
			})
		}
		wg.Wait()
		close(results)
		admitted := 0
		for err := range results {
			if err == nil {
				admitted++
			} else {
				assertConfigurationBudgetError(t, err)
			}
		}
		if calls.Load() != 1 || admitted != 1 {
			t.Fatal("configuration requests overspent the last attempt", calls.Load(), admitted)
		}
	})
}

func TestConfigurationConcurrentLastRequest(t *testing.T) {
	t.Run("memory", func(t *testing.T) { configurationLastRequest(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		st, _ := multipartCopyPGStore(t)
		configurationLastRequest(t, st)
	})
}

func TestProtectionReadsReserveEveryNativeAttempt(t *testing.T) {
	for _, kind := range []string{"retention", "legal-hold"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			f := newMultipartCopyIntegrationWithTransfer(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !r.URL.Query().Has("object-lock") {
					t.Error("unreserved native attempt", r.URL)
				}
				_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
			}), objectstorage.Config{}, 0)
			configureRequestBudget(t, f, 1)
			var err error
			if kind == "retention" {
				_, err = f.client.GetObjectRetention(t.Context(), &awss3.GetObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String("key"), VersionId: aws.String("null")})
			} else {
				_, err = f.client.GetObjectLegalHold(t.Context(), &awss3.GetObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String("key"), VersionId: aws.String("null")})
			}
			assertConfigurationBudgetError(t, err)
			if calls.Load() != 1 {
				t.Fatal("protection preflight was not metered per attempt", calls.Load())
			}
		})
	}
}

func TestAcceptedConfigurationAndCleanupContinueAtCeiling(t *testing.T) {
	t.Run("versioning", func(t *testing.T) {
		st := state.NewMemStore()
		native := &bucketVersioningHTTPFixture{}
		var calls atomic.Int32
		f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			native.mu.Lock()
			empty := native.status == "" && r.Method == http.MethodGet
			native.mu.Unlock()
			if empty {
				_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
				return
			}
			native.serve(t, w, r)
		}), objectstorage.Config{}, 0)
		configureRequestBudget(t, f, 1)
		_, err := f.client.PutBucketVersioning(t.Context(), &awss3.PutBucketVersioningInput{Bucket: aws.String("assets"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}})
		if err != nil || calls.Load() != 4 {
			t.Fatal("accepted reconciliation stopped at request ceiling", err, calls.Load())
		}
		usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
		if err != nil || state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()).RequestCount != 4 {
			t.Fatal("accepted reconciliation lost its request measurements", usage, err)
		}
		_, err = f.client.GetBucketVersioning(t.Context(), &awss3.GetBucketVersioningInput{Bucket: aws.String("assets")})
		assertConfigurationBudgetError(t, err)
	})
	t.Run("encryption cleanup", func(t *testing.T) {
		st := state.NewMemStore()
		native := &bucketEncryptionNativeHTTP{}
		var calls atomic.Int32
		f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			native.serve(t, w, r)
		}), objectstorage.Config{}, 0)
		configureRequestBudget(t, f, 1)
		if err := putConfigurationAES256(t, f.client); err != nil || calls.Load() != 3 {
			t.Fatal("accepted encryption change could not reconcile", err, calls.Load())
		}
		_, err := f.client.GetBucketEncryption(t.Context(), &awss3.GetBucketEncryptionInput{Bucket: aws.String("assets")})
		assertConfigurationBudgetError(t, err)
		f.handler.enabled = func() bool { return false }
		if _, err = f.client.DeleteBucketEncryption(t.Context(), &awss3.DeleteBucketEncryptionInput{Bucket: aws.String("assets")}); err != nil {
			t.Fatal("exhausted budget or disabled ingress blocked cleanup", err)
		}
		j, err := st.GetObjectBucketEncryption(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
		if err != nil || j.State != "ready" || !j.Encryption.Empty() || calls.Load() != 4 {
			t.Fatal("cleanup did not settle or measure its native read", j, err, calls.Load())
		}
	})
}

func putConfigurationAES256(t *testing.T, client *awss3.Client) error {
	t.Helper()
	_, err := client.PutBucketEncryption(t.Context(), &awss3.PutBucketEncryptionInput{Bucket: aws.String("assets"), ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{Rules: []types.ServerSideEncryptionRule{{ApplyServerSideEncryptionByDefault: &types.ServerSideEncryptionByDefault{SSEAlgorithm: types.ServerSideEncryptionAes256}}}}})
	return err
}
