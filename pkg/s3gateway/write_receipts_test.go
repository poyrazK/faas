package s3gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 537
func TestGatewayWriteReceiptPolling(t *testing.T) {
	h, st, provider := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) {
		t.Error("receipt read contacted provider")
		return nil, errors.New("unexpected")
	})
	c, err := st.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: st.bucket.AccountID, AppID: st.bucket.AppID, BucketID: st.bucket.ID, SubjectID: st.credential.ID, Key: "key", Bytes: 4, Status: "pending"}, h.registry.Accounting)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h.enabled = func() bool { return false }
	// A placement mismatch must not prevent reading durable receipt metadata.
	st.bucket.BackendFingerprint = "unavailable"
	url := "http://s3.gregale.dev/assets/key?gregale-upload-id=" + c.ID
	for _, presigned := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := signedGatewayRequest(t, "GET", url, nil, "UNSIGNED-PAYLOAD")
		if presigned {
			r = presignedGatewayRequest(t, "GET", url, nil)
		}
		h.ServeHTTP(w, r)
		var receipt api.ObjectWriteReceipt
		if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != 200 || receipt.ID != c.ID || receipt.Status != "pending" || receipt.Operation != "put" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Retry-After") != "30" {
			t.Fatal(w.Code, w.Body.String(), err)
		}
		for _, private := range []string{"subject_id", "recovery", "fingerprint", "physical", "source_key", "sealed"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private receipt exposed", w.Body.String())
			}
		}
	}
	for _, tc := range []struct {
		name, method, url string
		code              int
	}{
		{"foreign bucket", "GET", "http://s3.gregale.dev/foreign/key?gregale-upload-id=" + c.ID, 404},
		{"foreign key", "GET", "http://s3.gregale.dev/assets/other?gregale-upload-id=" + c.ID, 404},
		{"malformed ID", "GET", "http://s3.gregale.dev/assets/key?gregale-upload-id=bad", 404},
		{"missing ID", "GET", "http://s3.gregale.dev/assets/key?gregale-upload-id=", 404},
		{"duplicate ID", "GET", url + "&gregale-upload-id=" + c.ID, 400},
		{"mixed operation", "GET", url + "&tagging=", 400},
		{"write method", "PUT", url, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, signedGatewayRequest(t, tc.method, tc.url, nil, "UNSIGNED-PAYLOAD"))
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	owner := st.credential.ID
	st.credential.ID = uuid.NewString()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedGatewayRequest(t, "GET", url, nil, "UNSIGNED-PAYLOAD"))
	if w.Code != 404 {
		t.Fatal("foreign credential read receipt", w.Code)
	}
	st.credential.ID, st.credential.Permission = owner, state.ObjectBucketPermissionRead
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedGatewayRequest(t, "GET", url, nil, "UNSIGNED-PAYLOAD"))
	if w.Code != 403 {
		t.Fatal("read-only credential read write receipt", w.Code)
	}
	st.credential.Permission = state.ObjectBucketPermissionWrite
	if _, err = st.DispatchTrackedObjectUpload(t.Context(), st.bucket.AccountID, st.bucket.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag = "completed", `"confirmed"`
	if _, err = st.FinishTrackedObjectUpload(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedGatewayRequest(t, "GET", url, nil, "UNSIGNED-PAYLOAD"))
	var receipt api.ObjectWriteReceipt
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Status != "completed" || receipt.ETag != c.ETag || w.Header().Get("Retry-After") != "" {
		t.Fatal(w.Body.String(), err)
	}
	after, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
	if err != nil || after.Authorizations != before.Authorizations || after.Buckets[0].GrantedBytes != before.Buckets[0].GrantedBytes || len(provider.presignRequests) != 0 {
		t.Fatal("polling spent quota", after, err)
	}
}
