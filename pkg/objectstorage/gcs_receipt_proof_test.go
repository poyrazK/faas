package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"google.golang.org/api/option"
)

// adr: 628
func TestGCSXMLReceiptProofRejectsAmbiguousHeaders(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation string
		change     func(http.Header)
		invalid    bool
		kms        string
	}{
		{name: "current"},
		{name: "retained", generation: "123"},
		{name: "wrong generation", generation: "124", invalid: true},
		{name: "noncanonical generation", change: func(h http.Header) { h.Set("X-Goog-Generation", "0123") }, invalid: true},
		{name: "duplicate generation", change: func(h http.Header) { h.Add("X-Goog-Generation", "123") }, invalid: true},
		{name: "missing etag", change: func(h http.Header) { h.Del("ETag") }, invalid: true},
		{name: "duplicate receipt", change: func(h http.Header) { h.Add("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey, uuid.NewString()) }, invalid: true},
		{name: "missing length", change: func(h http.Header) { h.Del("Content-Length") }, invalid: true},
		{name: "foreign version", change: func(h http.Header) { h.Set("X-Amz-Version-Id", "foreign") }, invalid: true},
		{name: "native CMEK", change: func(h http.Header) { h.Set("X-Goog-Encryption-Kms-Key-Name", "native-key") }, kms: "native-key"},
		{name: "native CSEK", change: func(h http.Header) { h.Set("X-Goog-Encryption-Algorithm", "AES256") }, kms: "unhandled-customer-key"},
		{name: "duplicate CMEK", change: func(h http.Header) {
			h.Add("X-Goog-Encryption-Kms-Key-Name", "a")
			h.Add("X-Goog-Encryption-Kms-Key-Name", "b")
		}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := uuid.NewString()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "HEAD" || r.URL.Path != "/physical/key" || r.URL.Query().Get("generation") != tc.generation || r.Header.Get("Accept-Encoding") != "gzip" {
					t.Error("incorrect proof request")
				}
				h := w.Header()
				h.Set("ETag", `"xml-etag"`)
				h.Set("X-Goog-Generation", "123")
				h.Set("Content-Length", "7")
				h.Set("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
				if tc.change != nil {
					tc.change(h)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			p := testGCS(server.URL, &googleGCSStore{})
			proof, err := p.gcsXMLProofObject(t.Context(), "physical", "key", tc.generation)
			if tc.invalid {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("ambiguous native proof accepted", err)
				}
				return
			}
			if err != nil || proof.ETag != `"xml-etag"` || proof.Version != 123 || proof.Size != 7 || proof.Metadata[ReservedUploadReceiptMetadataKey] != receipt || proof.KMSKeyName != tc.kms {
				t.Fatal(proof, err)
			}
			if tc.generation == "" && tc.kms == "" {
				confirmed, err := p.ConfirmTrackedObject(t.Context(), "physical", "key", receipt, 7)
				if err != nil || confirmed.ETag != proof.ETag {
					t.Fatal(confirmed, err)
				}
			}
		})
	}
}

// adr: 628
func TestGCSHistoricalReceiptUsesMeteredXMLProof(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "allowed", true: "budget denied"}[denied], func(t *testing.T) {
			receipt, requests, reservations := uuid.NewString(), 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if reservations != requests {
					t.Error("proof RPC was not reserved")
				}
				if r.Method == "GET" {
					if r.URL.Query().Get("versions") != "true" {
						t.Error("history was not complete")
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"name": "key", "size": "7", "generation": "123", "etag": "json-etag", "metadata": map[string]string{ReservedUploadReceiptMetadataKey: receipt}}}})
					return
				}
				if r.Method != "HEAD" || r.URL.Path != "/physical/key" || r.URL.Query().Get("generation") != "123" {
					t.Error("history proof did not select exact generation")
				}
				w.Header().Set("Content-Length", "7")
				w.Header().Set("ETag", `"xml-etag"`)
				w.Header().Set("X-Goog-Generation", "123")
				w.Header().Set("X-Goog-Meta-"+ReservedUploadReceiptMetadataKey, receipt)
			}))
			defer server.Close()
			client, err := storage.NewClient(t.Context(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"), storage.WithDisabledClientMetrics())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			p := testGCS(server.URL, &googleGCSStore{client: client})
			page, err := p.ConfirmTrackedObjectHistory(t.Context(), "physical", ObjectHistoryProofRequest{Key: "key", Receipt: receipt, SizeBytes: 7, BeforeRequest: func(context.Context) error {
				reservations++
				if denied && reservations == 2 {
					return ErrConflict
				}
				return nil
			}})
			if denied {
				if !errors.Is(err, ErrConflict) || requests != 1 || page.UploadResult.ETag != "" {
					t.Fatal(page, err, requests)
				}
			} else if err != nil || requests != 2 || page.UploadResult.ETag != `"xml-etag"` || page.UploadResult.ProviderVersionID != "123" {
				t.Fatal(page, err, requests)
			}
		})
	}
}
