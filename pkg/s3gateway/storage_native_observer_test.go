package s3gateway

import (
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/e2etest/storagefixture"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStorageNativeIndependentObserver(t *testing.T) {
	store := state.NewMemStore()
	f := newMultipartCopyIntegrationWithProvider(t, store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
			t.Error("unexpected provider operation from independent observer")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListBucketResult><Name>physical</Name><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	gateway := storagefixture.NewGateway(t, "127.0.0.1:0")
	endpoint, _ := url.Parse(gateway.URL)
	f.handler.host = endpoint.Host
	gateway.SetHandler(f.handler)
	probe := func(access, secret string, want int) {
		t.Helper()
		request, err := storagefixture.SignedRead(t.Context(), gateway.URL, "assets", "", aws.Credentials{AccessKeyID: access, SecretAccessKey: secret})
		if err != nil {
			t.Fatal("independent observer could not sign")
		}
		response, err := gateway.Client().Do(request)
		if err != nil {
			t.Fatal("independent observer TLS request failed")
		}
		_, copyErr := io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if copyErr != nil || response.StatusCode != want {
			t.Fatalf("independent observer: status=%d want=%d", response.StatusCode, want)
		}
	}
	probe(testAccess, testSecret, http.StatusOK)
	probe(testAccess, "wrong-signing-secret", http.StatusForbidden)
	rows, err := store.ListObjectS3Credentials(t.Context(), f.bucket.AccountID, f.bucket.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal("observer fixture credential inventory missing")
	}
	if err := store.RevokeObjectS3Credential(t.Context(), f.bucket.AccountID, f.bucket.ID, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	probe(testAccess, testSecret, http.StatusForbidden)
	const replacement = "GRGAAAAAAAAAAAAAAAAB"
	if _, err := store.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: uuid.NewString(), AccountID: f.bucket.AccountID,
		BucketID: f.bucket.ID, AccessKeyID: replacement, SecretSealed: []byte("sealed"), KID: "test", Label: "control",
		Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}, 10); err != nil {
		t.Fatal(err)
	}
	probe(replacement, testSecret, http.StatusOK)
}
