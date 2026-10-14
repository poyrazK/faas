package objectstorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// adr: 1038
func TestS3ObjectAttributesSparsePartPagination(t *testing.T) {
	var calls atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !r.URL.Query().Has("attributes") || r.Header.Get("X-Amz-Max-Parts") != "1" {
			t.Error("attribute selection or page limit lost", r.URL, r.Header)
		}
		marker, number, next, truncated := "0", 2, "<NextPartNumberMarker>2</NextPartNumberMarker>", true
		if r.Header.Get("X-Amz-Part-Number-Marker") == "2" {
			marker, number, next, truncated = "2", 7, "", false
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(w, `<GetObjectAttributesResponse><ObjectParts><PartsCount>2</PartsCount><MaxParts>1</MaxParts><PartNumberMarker>%s</PartNumberMarker>%s<IsTruncated>%t</IsTruncated><Part><PartNumber>%d</PartNumber><Size>5</Size></Part></ObjectParts></GetObjectAttributesResponse>`, marker, next, truncated, number)
	})).(ObjectAttributesProvider)
	input := ObjectAttributesRequest{Key: "key", Attributes: []string{"ObjectParts"}, MaxParts: 1}
	first, err := p.GetObjectAttributes(t.Context(), "bucket", input)
	if err != nil || first.Parts == nil || !first.Parts.Truncated || first.Parts.NextMarker != 2 || first.Parts.Parts[0].Number != 2 {
		t.Fatal(first, err)
	}
	input.PartNumberMarker = first.Parts.NextMarker
	last, err := p.GetObjectAttributes(t.Context(), "bucket", input)
	if err != nil || last.Parts == nil || last.Parts.Truncated || last.Parts.Count != 2 || last.Parts.Parts[0].Number != 7 || calls.Load() != 2 {
		t.Fatal(last, err, calls.Load())
	}
}

// adr: 1038
func TestObjectAttributesDoNotSerializeNativeVersion(t *testing.T) {
	for _, v := range []any{ObjectAttributesRequest{ProviderVersionID: "private-version"}, ObjectAttributes{ProviderVersionID: "private-version"}, ObjectAttributesDeleteMarker{ProviderVersionID: "private-version"}} {
		b, err := json.Marshal(v)
		if err != nil || strings.Contains(string(b), "private-version") || strings.Contains(string(b), "ProviderVersionID") {
			t.Fatal("private provider version serialized", string(b), err)
		}
	}
}

// adr: 1038
func TestS3ObjectAttributesDeleteMarkerProof(t *testing.T) {
	for _, tc := range []struct {
		name, expected, returned string
		status                   int
		duplicate, valid         bool
	}{
		{"current", "", "native-marker", 404, false, true},
		{"selected", "native-marker", "native-marker", 405, false, true},
		{"wrong version", "native-marker", "other", 405, false, false},
		{"wrong status", "native-marker", "native-marker", 404, false, false},
		{"duplicate version", "native-marker", "native-marker", 405, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("versionId") != tc.expected {
					t.Error("native version selection lost")
				}
				w.Header().Set("Content-Type", "application/xml")
				w.Header().Set("X-Amz-Delete-Marker", "true")
				w.Header().Add("X-Amz-Version-Id", tc.returned)
				if tc.duplicate {
					w.Header().Add("X-Amz-Version-Id", tc.returned)
				}
				w.Header().Set("Last-Modified", "Tue, 13 Oct 2026 10:00:00 GMT")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `<Error><Code>MethodNotAllowed</Code></Error>`)
			})).(ObjectAttributesProvider)
			_, err := p.GetObjectAttributes(t.Context(), "bucket", ObjectAttributesRequest{Key: "key", ProviderVersionID: tc.expected, Attributes: []string{"ETag"}, MaxParts: 1})
			var marker *ObjectAttributesDeleteMarker
			if tc.valid {
				if !errors.As(err, &marker) || marker.ProviderVersionID != tc.returned || marker.LastModified.IsZero() {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("attribute error retried", calls.Load())
			}
		})
	}
}
