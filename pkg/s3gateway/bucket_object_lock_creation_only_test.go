// adr: 20261119065000
package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// On a creation-only backend PutObjectLockConfiguration for a bucket created
// without Object Lock answers AWS's InvalidBucketState, records no intent and
// sends no native PUT.
func TestBucketObjectLockCreationOnlyInvalidBucketState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flags    objectstorage.ObjectLockConfig
		wantCode string
	}{
		{"creation only", objectstorage.ObjectLockConfig{Enabled: true, CreationOnly: true}, "InvalidBucketState"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := state.NewMemStore()
			var puts atomic.Int32
			f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == http.MethodPut:
					puts.Add(1)
				case r.URL.Query().Has("versioning"):
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
				default:
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
				}
			}), objectstorage.Config{}, 0)
			policy := f.handler.registry.Accounting
			r, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "local"}, Backends: []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "integration", Endpoint: f.nativeEndpoint, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", ObjectLock: tc.flags}}}, func(string) string { return "local-provider-test-credential" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
			if err != nil {
				t.Fatal(err)
			}
			f.handler.registry = r
			public := httptest.NewTLSServer(f.handler)
			defer public.Close()
			u, _ := url.Parse(public.URL)
			f.handler.host = u.Host
			client := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""), HTTPClient: public.Client()}, func(o *awss3.Options) {
				o.BaseEndpoint = aws.String(public.URL)
				o.UsePathStyle = true
				o.RetryMaxAttempts = 1
			})
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: &types.ObjectLockConfiguration{ObjectLockEnabled: types.ObjectLockEnabledEnabled}})
			assertSDKErrorCode(t, err, tc.wantCode)
			if puts.Load() != 0 {
				t.Fatalf("refusal sent %d native PUTs", puts.Load())
			}
			j, err := st.GetObjectBucketObjectLock(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if err != nil || j.Revision != 0 || j.EnabledRequired {
				t.Fatalf("refusal recorded intent: %+v (%v)", j, err)
			}
		})
	}
}
