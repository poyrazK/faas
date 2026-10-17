package s3gateway

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func gatewayAdmissionFixture(t *testing.T, wrap func(http.Handler) http.Handler, recovery ...bool) (*Handler, *gatewayReceiptStore, *objectnode.Store) {
	t.Helper()
	settlements := len(recovery) != 0 && recovery[0]
	var s *objectnode.Store
	var node *DiskHandler
	if settlements {
		s, node = recoveryDiskFixture(t)
	} else {
		s, node = diskFixture(t)
	}
	if err := s.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	setDiskAdmissionPressure(t, s, false)
	var upstream http.Handler = node
	if wrap != nil {
		upstream = wrap(node)
	}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	p, err := objectstorage.NewS3(objectstorage.BackendConfig{Driver: "s3", Endpoint: server.URL, PathStyle: true, S3Region: "us-east-1", OwnedNodeReceipts: true, OwnedNodeSettlements: settlements, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}, func(name string) string {
		if name == "" {
			return ""
		}
		return diskTestCredential
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.(objectstorage.ObjectWriteAdmissionChecker).CheckObjectWriteAdmission(t.Context(), "physical", "put", 4); err != nil {
		t.Fatal("fixture pressure probe", err)
	}
	h, st, _ := newGatewayReceiptHandler(t, http.DefaultTransport.RoundTrip)
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &h.registry.Accounting, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "test", Region: "us-east-1", Namespace: "test"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"test": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil || backend.Fingerprint != st.bucket.BackendFingerprint {
		t.Fatal("fixture placement changed", err)
	}
	h.registry = registry
	h.now = func() time.Time { return time.Now().UTC() }
	return h, st, s
}

func admissionGatewayRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	r := signedGatewayRequest(t, method, "http://s3.gregale.dev/assets/"+path, body, "UNSIGNED-PAYLOAD")
	if err := s3Signer().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return r
}

type unreadGatewayAdmissionBody struct{ read bool }

func (r *unreadGatewayAdmissionBody) Read([]byte) (int, error) {
	r.read = true
	return 0, errors.New("body read before admission")
}

func TestGatewayWritePressurePreflightAndUncertainRaces(t *testing.T) {
	for _, mode := range []string{"visible pressure", "pressure after hint", "lost committed response"} {
		t.Run(mode, func(t *testing.T) {
			var s *objectnode.Store
			var writes atomic.Int64
			h, st, store := gatewayAdmissionFixture(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/key") {
						writes.Add(1)
						if mode == "pressure after hint" {
							setDiskAdmissionPressure(t, s, true)
						}
						if mode == "lost committed response" {
							next.ServeHTTP(httptest.NewRecorder(), r)
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
					}
					next.ServeHTTP(w, r)
				})
			})
			s = store
			if mode == "visible pressure" {
				setDiskAdmissionPressure(t, s, true)
			}
			r := admissionGatewayRequest(t, "PUT", "key", []byte("data"))
			var unread *unreadGatewayAdmissionBody
			if mode == "visible pressure" {
				unread = &unreadGatewayAdmissionBody{}
				r.Body = io.NopCloser(unread)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 503 {
				t.Fatal(w.Code, w.Body.String())
			}
			usage, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			id := w.Header().Get("X-Gregale-Upload-ID")
			if mode == "visible pressure" {
				if id != "" || usage.Buckets[0].GrantedBytes != 0 || writes.Load() != 0 || unread.read || !strings.Contains(w.Body.String(), "SlowDown") || w.Header().Get("Retry-After") != "1" {
					t.Fatal("preflight created a hold, read the body or dispatched", id, usage, writes.Load(), unread.read, w.Body.String())
				}
				setDiskAdmissionPressure(t, s, false)
				recovered := httptest.NewRecorder()
				h.ServeHTTP(recovered, admissionGatewayRequest(t, "PUT", "key", []byte("data")))
				if recovered.Code != 200 {
					t.Fatal("new write did not recover", recovered.Code, recovered.Body.String())
				}
				return
			}
			c, err := st.GetObjectUploadReceipt(t.Context(), st.bucket.AccountID, st.bucket.AppID, "", st.credential.ID, id)
			if err != nil || c.WritePhase != state.ObjectUploadDispatched || c.Status != "pending" || usage.Buckets[0].GrantedBytes != 4 || writes.Load() != 1 {
				t.Fatal("uncertain dispatch lost its hold or replayed", c, usage, writes.Load(), err)
			}
			backend, _ := h.registry.Default("us-east-1")
			_, err = backend.Provider.(objectstorage.ObjectWriteConfirmer).ConfirmTrackedObject(t.Context(), "physical", "key", id, 4)
			if mode == "pressure after hint" && !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("absence became rejection proof", err)
			}
			if mode == "lost committed response" && err != nil {
				t.Fatal("lost acknowledgement lost durable receipt", err)
			}
		})
	}
}

func TestGatewayWritePressureMultipartDoesNotFreezeOrReserveNewWork(t *testing.T) {
	h, st, s := gatewayAdmissionFixture(t, nil)
	init := httptest.NewRecorder()
	h.ServeHTTP(init, admissionGatewayRequest(t, "POST", "key?uploads", nil))
	if init.Code != 200 {
		t.Fatal(init.Code, init.Body.String())
	}
	var started struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(init.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	setDiskAdmissionPressure(t, s, true)
	part := httptest.NewRecorder()
	h.ServeHTTP(part, admissionGatewayRequest(t, "PUT", "key?uploadId="+started.UploadID+"&partNumber=1", []byte("part")))
	if part.Code != 503 {
		t.Fatal(part.Code, part.Body.String())
	}
	setDiskAdmissionPressure(t, s, false)
	part = httptest.NewRecorder()
	h.ServeHTTP(part, admissionGatewayRequest(t, "PUT", "key?uploadId="+started.UploadID+"&partNumber=1", []byte("part")))
	if part.Code != 200 {
		t.Fatal(part.Code, part.Body.String())
	}
	setDiskAdmissionPressure(t, s, true)
	complete := httptest.NewRecorder()
	body := []byte("<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>" + part.Header().Get("ETag") + "</ETag></Part></CompleteMultipartUpload>")
	h.ServeHTTP(complete, admissionGatewayRequest(t, "POST", "key?uploadId="+started.UploadID, body))
	if complete.Code != 503 {
		t.Fatal(complete.Code, complete.Body.String())
	}
	u, err := st.GetObjectMultipartUpload(t.Context(), st.bucket.AccountID, st.bucket.AppID, st.bucket.ID, started.UploadID)
	if err != nil || u.State != state.ObjectMultipartActive || len(u.Parts) != 0 || u.CompletionDispatched {
		t.Fatal("pressure froze a new completion", u, err)
	}
	usage, err := st.ObjectUsage(t.Context(), st.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].GrantedBytes != 0 {
		t.Fatal("pressure created object capacity hold", usage, err)
	}
	abort := httptest.NewRecorder()
	h.ServeHTTP(abort, admissionGatewayRequest(t, "DELETE", "key?uploadId="+started.UploadID, nil))
	if abort.Code != 204 {
		t.Fatal("pressure blocked cleanup", abort.Code, abort.Body.String())
	}
}

func TestGatewayWritePressureFrozenCompletionResumes(t *testing.T) {
	var interrupt atomic.Bool
	interrupt.Store(true)
	h, st, s := gatewayAdmissionFixture(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Query().Has("uploadId") && interrupt.CompareAndSwap(true, false) {
				// Fault after manifest admission: leave the durable freeze in place.
				node := next.(*DiskHandler)
				floor := node.config.MinFreeBytes
				node.config.MinFreeBytes = 1 << 50
				defer func() { node.config.MinFreeBytes = floor }()
			}
			next.ServeHTTP(w, r)
		})
	})
	init := httptest.NewRecorder()
	h.ServeHTTP(init, admissionGatewayRequest(t, "POST", "key?uploads", nil))
	if init.Code != 200 {
		t.Fatal(init.Code, init.Body.String())
	}
	var started struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(init.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	part := httptest.NewRecorder()
	h.ServeHTTP(part, admissionGatewayRequest(t, "PUT", "key?uploadId="+started.UploadID+"&partNumber=1", []byte("part")))
	if part.Code != 200 {
		t.Fatal(part.Code, part.Body.String())
	}
	body := []byte("<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>" + part.Header().Get("ETag") + "</ETag></Part></CompleteMultipartUpload>")
	first := httptest.NewRecorder()
	h.ServeHTTP(first, admissionGatewayRequest(t, "POST", "key?uploadId="+started.UploadID, body))
	if first.Code != 503 {
		t.Fatal(first.Code, first.Body.String())
	}
	u, err := st.GetObjectMultipartUpload(t.Context(), st.bucket.AccountID, st.bucket.AppID, st.bucket.ID, started.UploadID)
	if err != nil || !state.ObjectMultipartIsCompleting(u.State) || !u.CompletionDispatched || len(u.Parts) != 1 {
		t.Fatal("fault did not retain frozen work", u, err)
	}
	setDiskAdmissionPressure(t, s, true)
	// Advance only the fixture's settlement clock past its retry lease.
	st.SetClockForTest(func() time.Time { return u.RetryAt.Add(time.Second) })
	resumed := httptest.NewRecorder()
	h.ServeHTTP(resumed, admissionGatewayRequest(t, "POST", "key?uploadId="+started.UploadID, body))
	if resumed.Code != 200 {
		t.Fatal("frozen work was throttled", resumed.Code, resumed.Body.String())
	}
	finished, err := st.GetObjectMultipartUpload(t.Context(), st.bucket.AccountID, st.bucket.AppID, st.bucket.ID, started.UploadID)
	if err != nil || finished.State != state.ObjectMultipartCompleted || finished.CompletionETag == "" {
		t.Fatal(finished, err)
	}
}
