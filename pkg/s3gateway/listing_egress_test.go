package s3gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// listingEgressMetrics adds the gateway-safety request reservation a
// provider listing takes before it calls the provider.
type listingEgressMetrics struct {
	readEgressMetrics
	requests int
}

func (m *listingEgressMetrics) ReserveObjectStorageGatewayRequest(context.Context, string, time.Time, api.ObjectStoragePolicy) error {
	m.requests++
	return nil
}

const listingTransferPrefix = "s3_gateway_listing_test"

// pacedDownloadBytes reads the bytes a limiter let through as downloads.
func pacedDownloadBytes(t *testing.T, l *objectstorage.TransferLimiter) int {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(l.Collectors()...)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != listingTransferPrefix+"_transfer_bytes_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "direction" && label.GetValue() == "download" {
					return int(metric.GetCounter().GetValue())
				}
			}
		}
	}
	t.Fatal("limiter has no download byte counter")
	return 0
}

type listingCase struct {
	name, root string
	path       func(uploadID string) string
}

// Every S3 listing the gateway serves: provider pages, owned multipart
// sessions and provider parts. Versions need a version-aware store and
// provider; TestGatewayVersionListingMetersEgress covers them.
var meteredListings = []listingCase{
	{"objects", "ListBucketResult", func(string) string { return "/assets?list-type=2" }},
	{"multipart uploads", "ListMultipartUploadsResult", func(string) string { return "/assets?uploads=" }},
	{"multipart parts", "ListPartsResult", func(id string) string { return "/assets/key?uploadId=" + id }},
}

func newListingEgressHandler(t *testing.T, mode string) (*Handler, *gatewayTestStore, *listingEgressMetrics, string) {
	t.Helper()
	h, store, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Error("listing requested a provider URL")
		return nil, errors.New("unexpected")
	})
	h.registry.Accounting.AccountingMode = mode
	metrics := &listingEgressMetrics{}
	h.requestMetrics = metrics
	h.transfers = objectstorage.NewTransferLimiter(1<<30, listingTransferPrefix)
	sessions := newGatewayMultipartStore()
	h.multipartStore = sessions
	id := uuid.NewString()
	sessions.uploads[id] = state.ObjectMultipartUpload{ID: id, AccountID: store.bucket.AccountID, AppID: store.bucket.AppID, BucketID: store.bucket.ID, Key: "key", ProviderUploadID: "provider", State: state.ObjectMultipartActive, ExpiresAt: h.now().Add(time.Hour), CreatedAt: h.now()}
	p.multipart = map[string]map[int32]objectstorage.MultipartPart{"provider": {1: {PartNumber: 1, ETag: `"one"`, SizeBytes: 5 << 20, LastModified: h.now()}}}
	return h, store, metrics, id
}

// adr: 20261031122757 — a listing body counts against the month's egress and
// is paced to the node's port share like an object body: reserved before it
// is sent under gateway-safety accounting, recorded as delivered otherwise.
func TestGatewayListingsMeterAndPaceEgress(t *testing.T) {
	for _, listing := range meteredListings {
		for _, mode := range []string{"", api.ObjectStorageGatewaySafetyV1} {
			t.Run(listing.name+"/"+mode, func(t *testing.T) {
				h, _, metrics, id := newListingEgressHandler(t, mode)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev"+listing.path(id), nil, "UNSIGNED-PAYLOAD"))
				sent := w.Body.Len()
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<"+listing.root) {
					t.Fatalf("listing = %d %s", w.Code, w.Body.String())
				}
				if w.Header().Get("Content-Length") != strconv.Itoa(sent) {
					t.Fatalf("Content-Length %q for a %d-byte body", w.Header().Get("Content-Length"), sent)
				}
				reserved, delivered := int64(0), int64(sent)
				if mode != "" {
					reserved, delivered = int64(sent), 0
				}
				if metrics.reserved != reserved || metrics.delivered != delivered {
					t.Fatalf("%d-byte listing reserved %d and recorded %d delivered, want %d and %d", sent, metrics.reserved, metrics.delivered, reserved, delivered)
				}
				if paced := pacedDownloadBytes(t, h.transfers); paced != sent {
					t.Fatalf("%d-byte listing took %d bytes of the port share", sent, paced)
				}
			})
		}
	}
}

// adr: 20261031122757 — at the egress limit every listing is refused: one
// the month's admission refuses before it lists, one whose body would cross
// the limit before its first byte leaves.
func TestGatewayListingsRefusedAtEgressLimit(t *testing.T) {
	limit := &state.ObjectStorageLimitError{Kind: "egress_bytes", Limit: 10 << 30, Observed: 10 << 30, Cause: state.ErrObjectBudget}
	for _, listing := range meteredListings {
		for _, refusal := range []string{"admission", "reservation"} {
			t.Run(listing.name+"/"+refusal, func(t *testing.T) {
				h, store, metrics, id := newListingEgressHandler(t, api.ObjectStorageGatewaySafetyV1)
				if refusal == "admission" {
					store.admitErr = limit
				} else {
					metrics.reserveError = limit
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev"+listing.path(id), nil, "UNSIGNED-PAYLOAD"))
				body := w.Body.String()
				if w.Code != http.StatusPaymentRequired || !strings.Contains(body, "<Code>AccountProblem</Code>") || !strings.Contains(body, "egress_bytes") || strings.Contains(body, listing.root) {
					t.Fatalf("listing at the egress limit = %d %s", w.Code, body)
				}
				if metrics.reserved != 0 || pacedDownloadBytes(t, h.transfers) != 0 {
					t.Fatalf("refused listing reserved %d bytes or sent a body", metrics.reserved)
				}
			})
		}
	}
}

// admittedVersionStore admits every request, so a gateway-safety version
// listing reaches the provider without a fresh usage report.
type admittedVersionStore struct {
	Store
	state.ObjectVersionReferenceStore
}

func (admittedVersionStore) AdmitObjectURL(context.Context, string, string, string, int64, bool, api.ObjectStoragePolicy) error {
	return nil
}

// adr: 20261031122757
func TestGatewayVersionListingMetersEgress(t *testing.T) {
	for _, mode := range []string{"", api.ObjectStorageGatewaySafetyV1} {
		t.Run(mode, func(t *testing.T) {
			p := &publicVersionHTTPFixture{}
			f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
			f.handler.store = admittedVersionStore{Store: f.handler.store, ObjectVersionReferenceStore: f.store.(state.ObjectVersionReferenceStore)}
			f.handler.registry.Accounting.AccountingMode = mode
			metrics := &listingEgressMetrics{}
			f.handler.requestMetrics = metrics
			f.handler.transfers = objectstorage.NewTransferLimiter(1<<30, listingTransferPrefix)
			w := rawPublicVersionRead(t, f, http.MethodGet, "/assets?versions&max-keys=3")
			sent := w.Body.Len()
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<ListVersionsResult") || p.listCalls != 1 {
				t.Fatalf("version listing = %d %s", w.Code, w.Body.String())
			}
			reserved, delivered := int64(0), int64(sent)
			if mode != "" {
				reserved, delivered = int64(sent), 0
			}
			if metrics.reserved != reserved || metrics.delivered != delivered {
				t.Fatalf("%d-byte version listing reserved %d and recorded %d delivered", sent, metrics.reserved, metrics.delivered)
			}
			if paced := pacedDownloadBytes(t, f.handler.transfers); paced != sent {
				t.Fatalf("%d-byte version listing took %d bytes of the port share", sent, paced)
			}
		})
	}
}
