// adr: 20261025115958

package s3gateway

import (
	"crypto/md5" // #nosec G501 -- S3 Content-MD5 fixture.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestGatewayDeleteObjectsRunsEntriesConcurrently pins ADR-20261025115958: a
// bulk delete no longer sends its entries to the provider one at a time, runs
// at most ObjectDeletionBulkConcurrency at once, and answers in request order.
func TestGatewayDeleteObjectsRunsEntriesConcurrently(t *testing.T) {
	handler, _, provider := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("DeleteObjects must use the provider abstraction")
		return nil, nil
	})
	provider.deleteDelay = 20 * time.Millisecond
	const entries = 3 * api.ObjectDeletionBulkConcurrency
	var body strings.Builder
	body.WriteString("<Delete>")
	for i := range entries {
		fmt.Fprintf(&body, "<Object><Key>k%02d</Key></Object>", i)
	}
	body.WriteString("</Delete>")
	payload := []byte(body.String())
	sum := sha256.Sum256(payload)
	request := signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets?delete=", payload, hex.EncodeToString(sum[:]))
	md5sum := md5.Sum(payload) // #nosec G401 -- S3 Content-MD5 fixture.
	request.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(md5sum[:]))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("DeleteObjects = %d %s", recorder.Code, recorder.Body.String())
	}
	if provider.maxDeleting < 2 || provider.maxDeleting > api.ObjectDeletionBulkConcurrency {
		t.Fatalf("%d deletes ran at once, want 2..%d", provider.maxDeleting, api.ObjectDeletionBulkConcurrency)
	}
	got := recorder.Body.String()
	last := -1
	for i := range entries {
		at := strings.Index(got, fmt.Sprintf("<Key>k%02d</Key>", i))
		if at <= last {
			t.Fatalf("entry %d is missing or out of order: %s", i, got)
		}
		last = at
	}
}
