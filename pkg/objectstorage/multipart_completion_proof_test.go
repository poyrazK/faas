package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGCSAbortRequiresCompleteNativeProof(t *testing.T) {
	for _, body := range []string{
		``,
		`<ListPartsResult/>`,
		`<Error><Code>InternalError</Code></Error>`,
		`<ListPartsResult><IsTruncated>false</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker></ListPartsResult>`,
	} {
		t.Run(body, func(t *testing.T) {
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			t.Cleanup(native.Close)
			p := testGCS(native.URL, &fakeGCSStore{})
			p.httpClient = native.Client()
			err := VerifyMultipartAbort(t.Context(), p, "physical", MultipartAbortRequest{Key: "key", ProviderUploadID: "native-upload"})
			t.Logf("native body=%q abort absence proof=%v", body, err)
			if !errors.Is(err, ErrUnavailable) {
				t.Error("incomplete native listing proved abort and permits capacity release")
			}
		})
	}
}

func TestGCSPartsListingRejectsInvalidBoundaries(t *testing.T) {
	part := `<Part><PartNumber>2</PartNumber><ETag>&quot;part&quot;</ETag><Size>10</Size><LastModified>2026-11-01T00:00:00Z</LastModified></Part>`
	for _, tc := range []struct{ name, body string }{
		{"duplicate", `<IsTruncated>false</IsTruncated>` + part + part},
		{"before marker", `<IsTruncated>false</IsTruncated>` + strings.Replace(part, `<PartNumber>2</PartNumber>`, `<PartNumber>1</PartNumber>`, 1)},
		{"out of order", `<IsTruncated>false</IsTruncated>` + strings.Replace(part, `<PartNumber>2</PartNumber>`, `<PartNumber>3</PartNumber>`, 1) + part},
		{"excess rows", `<IsTruncated>false</IsTruncated>` + part + part + part},
		{"empty truncated", `<IsTruncated>true</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker>`},
		{"skipped boundary", `<IsTruncated>true</IsTruncated><NextPartNumberMarker>3</NextPartNumberMarker>` + part},
		{"missing continuation", `<IsTruncated>true</IsTruncated>` + part},
		{"wrong upload", `<IsTruncated>false</IsTruncated><UploadId>another-upload</UploadId>` + part},
		{"wrong bucket", `<IsTruncated>false</IsTruncated><Bucket>another-bucket</Bucket>` + part},
		{"wrong key", `<IsTruncated>false</IsTruncated><Key>another-key</Key>` + part},
		{"missing size", `<IsTruncated>false</IsTruncated>` + strings.Replace(part, `<Size>10</Size>`, ``, 1)},
		{"invalid etag", `<IsTruncated>false</IsTruncated>` + strings.Replace(part, `&quot;part&quot;`, ` `, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `<ListPartsResult>`+tc.body+`</ListPartsResult>`)
			}))
			t.Cleanup(native.Close)
			p := testGCS(native.URL, &fakeGCSStore{})
			p.httpClient = native.Client()
			page, err := p.ListMultipartParts(t.Context(), "physical", MultipartListPartsRequest{Key: "key", ProviderUploadID: "upload", PartNumberMarker: 1, Limit: 2})
			if !errors.Is(err, ErrUnavailable) || len(page.Items) != 0 || page.NextPartNumberMarker != 0 {
				t.Fatal("unsafe parts evidence accepted", page, err)
			}
		})
	}
}

func TestGCSMultipartDiscoveryRejectsInvalidBoundaries(t *testing.T) {
	row := `<Upload><Key>key</Key><UploadId>upload</UploadId></Upload>`
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"duplicate", `<IsTruncated>false</IsTruncated>` + row + row, ErrUnavailable},
		{"ambiguous exact key", `<IsTruncated>false</IsTruncated>` + row + strings.Replace(row, `>upload<`, `>another<`, 1), ErrConflict},
		{"prefix mismatch", `<IsTruncated>false</IsTruncated>` + strings.Replace(row, `>key<`, `>other<`, 1), ErrUnavailable},
		{"out of order", `<IsTruncated>false</IsTruncated>` + strings.Replace(row, `>key<`, `>key-z<`, 1) + row, ErrUnavailable},
		{"empty truncated", `<IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextUploadIdMarker>upload</NextUploadIdMarker>`, ErrUnavailable},
		{"missing continuation", `<IsTruncated>true</IsTruncated>` + row, ErrUnavailable},
		{"skipped upload", `<IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextUploadIdMarker>another</NextUploadIdMarker>` + row, ErrUnavailable},
		{"grouped entries", `<IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>key/</Prefix></CommonPrefixes>`, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creates atomic.Int32
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					creates.Add(1)
					w.WriteHeader(500)
					return
				}
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult>`+tc.body+`</ListMultipartUploadsResult>`)
			}))
			t.Cleanup(native.Close)
			p := testGCS(native.URL, &fakeGCSStore{})
			p.httpClient = native.Client()
			_, err := p.EnsureMultipartUpload(t.Context(), "physical", MultipartCreateRequest{Key: "key", SessionID: "session", SizeBytes: 10})
			if !errors.Is(err, tc.want) || creates.Load() != 0 {
				t.Fatal("invalid discovery allowed initiation", err, creates.Load())
			}
		})
	}
}

func TestGCSMultipartDiscoveryDecodesAndRetainsPairedCursor(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Has("encoding-type") {
			t.Error("invalid discovery dispatch", r.Method, r.URL)
		}
		if q.Get("key-marker") == "" {
			// A reply that declares url encoding is still decoded.
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><Encoding-Type>url</Encoding-Type><IsTruncated>true</IsTruncated><Upload><Key>key%2F%2B%25</Key><UploadId>opaque/+%</UploadId></Upload><NextKeyMarker>key%2F%2B%25</NextKeyMarker><NextUploadIdMarker>opaque/+%</NextUploadIdMarker></ListMultipartUploadsResult>`)
			return
		}
		if q.Get("key-marker") != "key/+%" || q.Get("upload-id-marker") != "opaque/+%" {
			t.Error("discovery changed its paired cursor", q)
		}
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	}))
	t.Cleanup(native.Close)
	p := testGCS(native.URL, &fakeGCSStore{})
	p.httpClient = native.Client()
	id, err := p.EnsureMultipartUpload(t.Context(), "physical", MultipartCreateRequest{Key: "key/+%", SessionID: "session", SizeBytes: 10})
	if err != nil || id != "opaque/+%" || calls.Load() != 2 {
		t.Fatal("valid multipart discovery failed", id, err, calls.Load())
	}
}

func TestGCSInitiationRequiresCompleteDiscovery(t *testing.T) {
	for _, body := range []string{`<ListMultipartUploadsResult/>`, `<Error><Code>InternalError</Code></Error>`, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextUploadIdMarker>existing</NextUploadIdMarker></ListMultipartUploadsResult>`} {
		t.Run(body, func(t *testing.T) {
			var creates atomic.Int32
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					creates.Add(1)
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>duplicate-upload</UploadId></InitiateMultipartUploadResult>`)
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(native.Close)
			p := testGCS(native.URL, &fakeGCSStore{})
			p.httpClient = native.Client()
			id, err := p.EnsureMultipartUpload(t.Context(), "physical", MultipartCreateRequest{Key: "key", SessionID: "accepted-session", SizeBytes: 10})
			t.Logf("native body=%q id=%q err=%v creates=%d", body, id, err, creates.Load())
			if !errors.Is(err, ErrUnavailable) || creates.Load() != 0 {
				t.Error("incomplete discovery allowed a fresh multipart initiation")
			}
		})
	}
}

func TestS3InventoryRejectsContradictoryCompletion(t *testing.T) {
	p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("versions") {
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><NextKeyMarker>remaining</NextKeyMarker><NextVersionIdMarker>v1</NextVersionIdMarker></ListVersionsResult>`)
			return
		}
		_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><NextContinuationToken>remaining-page</NextContinuationToken></ListBucketResult>`)
	})
	objects, err := p.(ObjectV2Lister).ListObjectsV2(t.Context(), "physical", ObjectListRequest{Limit: 1000})
	t.Logf("current-object inventory=%+v err=%v", objects, err)
	if !errors.Is(err, ErrUnavailable) {
		t.Error("contradictory current-object listing was accepted as complete")
	}
	versions, err := p.(ObjectVersionInventoryProvider).ListObjectVersions(t.Context(), "physical", "", 1000)
	t.Logf("all-version inventory=%+v err=%v", versions, err)
	if !errors.Is(err, ErrUnavailable) {
		t.Error("contradictory all-version listing was accepted as complete")
	}
}
