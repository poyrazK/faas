package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

type partObservationProvider struct {
	Provider
	page  MultipartPartsPage
	err   error
	calls int
}

func (p *partObservationProvider) ListMultipartParts(_ context.Context, bucket string, r MultipartListPartsRequest) (MultipartPartsPage, error) {
	p.calls++
	if bucket != "bucket" || r.Key != "key" || r.ProviderUploadID != "upload" || r.PartNumberMarker != 6 || r.Limit != 1 {
		return MultipartPartsPage{}, ErrInvalid
	}
	return p.page, p.err
}
func TestObserveMultipartPart(t *testing.T) {
	part := MultipartPart{PartNumber: 7, ETag: `"opaque"`, SizeBytes: 4}
	for _, tc := range []struct {
		name      string
		page      MultipartPartsPage
		err, want error
	}{
		{name: "candidate", page: MultipartPartsPage{Items: []MultipartPart{part}}},
		{name: "candidate with later page", page: MultipartPartsPage{Items: []MultipartPart{part}, NextPartNumberMarker: 7}},
		{name: "empty", want: ErrNotFound},
		{name: "missing upload", err: ErrNotFound, want: ErrNotFound},
		{name: "later part", page: MultipartPartsPage{Items: []MultipartPart{{PartNumber: 8, ETag: `"later"`, SizeBytes: 4}}}, want: ErrNotFound},
		{name: "earlier part", page: MultipartPartsPage{Items: []MultipartPart{{PartNumber: 6, ETag: `"old"`, SizeBytes: 4}}}, want: ErrUnavailable},
		{name: "empty truncated", page: MultipartPartsPage{NextPartNumberMarker: 7}, want: ErrUnavailable},
		{name: "wrong cursor", page: MultipartPartsPage{Items: []MultipartPart{part}, NextPartNumberMarker: 8}, want: ErrUnavailable},
		{name: "duplicate", page: MultipartPartsPage{Items: []MultipartPart{part, part}}, want: ErrUnavailable},
		{name: "invalid identity", page: MultipartPartsPage{Items: []MultipartPart{{PartNumber: 7, SizeBytes: 4}}}, want: ErrUnavailable},
		{name: "permission failure", err: ErrConfiguration, want: ErrConfiguration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &partObservationProvider{page: tc.page, err: tc.err}
			checks := 0
			got, err := ObserveMultipartPart(t.Context(), p, "bucket", MultipartPartObservationRequest{Key: "key", ProviderUploadID: "upload", PartNumber: 7, BeforeRequest: func(context.Context) error { checks++; return nil }})
			if !errors.Is(err, tc.want) || p.calls != 1 || checks != 1 {
				t.Fatalf("got %+v, %v; calls=%d checks=%d", got, err, p.calls, checks)
			}
			if err == nil && got != part {
				t.Fatalf("candidate changed: %+v", got)
			}
			if err != nil && got != (MultipartPart{}) {
				t.Fatalf("candidate leaked on error: %+v", got)
			}
		})
	}
}
func TestObserveMultipartPartRequiresAuthority(t *testing.T) {
	p := &partObservationProvider{}
	r := MultipartPartObservationRequest{Key: "key", ProviderUploadID: "upload", PartNumber: 7}
	if _, err := ObserveMultipartPart(t.Context(), p, "bucket", r); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	r.BeforeRequest = func(context.Context) error { return ErrConflict }
	if _, err := ObserveMultipartPart(t.Context(), p, "bucket", r); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ObserveMultipartPart(ctx, p, "bucket", r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatalf("unauthorized IO: %d", p.calls)
	}
}

func TestS3ObserveMultipartPartReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"candidate", `<Bucket>bucket</Bucket><Key>key</Key><UploadId>upload</UploadId><IsTruncated>false</IsTruncated><Part><PartNumber>7</PartNumber><ETag>"opaque"</ETag><Size>4</Size></Part>`, nil},
		{"wrong upload", `<UploadId>another</UploadId><IsTruncated>false</IsTruncated>`, ErrUnavailable},
		{"wrong key", `<Key>another</Key><IsTruncated>false</IsTruncated>`, ErrUnavailable},
		{"incomplete", `<Part><PartNumber>7</PartNumber><ETag>"opaque"</ETag><Size>4</Size></Part>`, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("uploadId") != "upload" || r.URL.Query().Get("part-number-marker") != "6" || r.URL.Query().Get("max-parts") != "1" {
					t.Errorf("unexpected native request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, "<ListPartsResult>"+tc.body+"</ListPartsResult>")
			})
			_, err := ObserveMultipartPart(t.Context(), p, "bucket", MultipartPartObservationRequest{Key: "key", ProviderUploadID: "upload", PartNumber: 7, BeforeRequest: func(context.Context) error { return nil }})
			if !errors.Is(err, tc.want) || calls.Load() != 1 {
				t.Fatalf("error=%v calls=%d", err, calls.Load())
			}
		})
	}
}
