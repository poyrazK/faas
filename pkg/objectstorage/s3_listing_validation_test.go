package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func listingFixture(t *testing.T, handler http.HandlerFunc) Provider {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	c := testBackend()
	c.Endpoint = upstream.URL
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestS3ObjectListingRejectsIncompleteProof(t *testing.T) {
	object := `<Contents><Key>folder/a</Key><Size>10</Size></Contents>`
	for _, tc := range []struct {
		name, body string
	}{
		{"missing completeness", ``},
		{"missing continuation", `<IsTruncated>true</IsTruncated>` + object},
		{"empty truncated page", `<IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken>`},
		{"repeated continuation", `<IsTruncated>true</IsTruncated><NextContinuationToken>current</NextContinuationToken>` + object},
		{"oversized continuation", `<IsTruncated>true</IsTruncated><NextContinuationToken>` + strings.Repeat("x", api.MaxObjectS3ListCursorBytes+1) + `</NextContinuationToken>` + object},
		{"missing size", `<IsTruncated>false</IsTruncated><Contents><Key>folder/a</Key></Contents>`},
		{"negative size", `<IsTruncated>false</IsTruncated><Contents><Key>folder/a</Key><Size>-1</Size></Contents>`},
		{"duplicate key", `<IsTruncated>false</IsTruncated>` + object + object},
		{"out of prefix", `<IsTruncated>false</IsTruncated><Contents><Key>other/a</Key><Size>10</Size></Contents>`},
		{"out of order", `<IsTruncated>false</IsTruncated><Contents><Key>folder/z</Key><Size>10</Size></Contents>` + object},
		{"unexpected prefix", `<IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>folder/sub/</Prefix></CommonPrefixes>`},
		{"excess entries", `<IsTruncated>false</IsTruncated>` + object + `<Contents><Key>folder/b</Key><Size>10</Size></Contents><Contents><Key>folder/c</Key><Size>10</Size></Contents>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := listingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, `<ListBucketResult>`+tc.body+`</ListBucketResult>`)
			})
			page, err := p.ListObjects(t.Context(), "gregale-test", "folder/", "current", 2)
			if !errors.Is(err, ErrUnavailable) || len(page.Items) != 0 || page.NextCursor != "" {
				t.Fatalf("unsafe listing accepted: %+v, %v", page, err)
			}
		})
	}
}

func TestS3PartsListingRejectsIncompleteProof(t *testing.T) {
	part := `<Part><PartNumber>2</PartNumber><ETag>&quot;part&quot;</ETag><Size>10</Size></Part>`
	for _, tc := range []struct {
		name, body string
	}{
		{"missing completeness", ``},
		{"duplicate part", `<IsTruncated>false</IsTruncated>` + part + part},
		{"before marker", `<IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&quot;part&quot;</ETag><Size>10</Size></Part>`},
		{"out of order", `<IsTruncated>false</IsTruncated><Part><PartNumber>3</PartNumber><ETag>&quot;part&quot;</ETag><Size>10</Size></Part>` + part},
		{"excess parts", `<IsTruncated>false</IsTruncated>` + part + `<Part><PartNumber>3</PartNumber><ETag>&quot;part&quot;</ETag><Size>10</Size></Part><Part><PartNumber>4</PartNumber><ETag>&quot;part&quot;</ETag><Size>10</Size></Part>`},
		{"empty truncated page", `<IsTruncated>true</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker>`},
		{"skipped continuation", `<IsTruncated>true</IsTruncated><NextPartNumberMarker>3</NextPartNumberMarker>` + part},
		{"whitespace etag", `<IsTruncated>false</IsTruncated><Part><PartNumber>2</PartNumber><ETag> </ETag><Size>10</Size></Part>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := listingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, `<ListPartsResult>`+tc.body+`</ListPartsResult>`)
			})
			page, err := p.ListMultipartParts(t.Context(), "gregale-test", MultipartListPartsRequest{Key: "key", ProviderUploadID: "private-upload", PartNumberMarker: 1, Limit: 2})
			if !errors.Is(err, ErrUnavailable) || len(page.Items) != 0 || page.NextPartNumberMarker != 0 {
				t.Fatalf("unsafe listing accepted: %+v, %v", page, err)
			}
		})
	}
}

func TestS3AbortRejectsMissingCompleteness(t *testing.T) {
	p := listingFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListPartsResult/>`)
	})
	if err := VerifyMultipartAbort(t.Context(), p, "gregale-test", MultipartAbortRequest{Key: "key", ProviderUploadID: "private-upload"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("malformed empty listing proved abort: %v", err)
	}
}

func TestS3MultipartInitiationDoesNotRetryUncertainCreate(t *testing.T) {
	var creates atomic.Int32
	p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.Method == http.MethodGet {
			body := `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated>`
			if creates.Load() > 0 {
				body += `<Upload><Key>key</Key><UploadId>original-upload</UploadId></Upload>`
			}
			_, _ = io.WriteString(w, body+`</ListMultipartUploadsResult>`)
			return
		}
		creates.Add(1)
		// The native upload was created, but its acknowledgment is unavailable.
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `<Error><Code>SlowDown</Code></Error>`)
	})
	request := MultipartCreateRequest{SessionID: "session", Key: "key", SizeBytes: 10}
	if _, err := p.EnsureMultipartUpload(t.Context(), "gregale-test", request); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if creates.Load() != 1 {
		t.Fatalf("uncertain initiation dispatched %d creates", creates.Load())
	}
	// Reconstruct the adapter and recover from the retained native identity.
	s3p := p.(*S3)
	c := testBackend()
	c.Endpoint = *s3p.client.Options().BaseEndpoint
	restarted, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	id, err := restarted.EnsureMultipartUpload(t.Context(), "gregale-test", request)
	if err != nil || id != "original-upload" || creates.Load() != 1 {
		t.Fatalf("recovery created another upload: %s, %v, creates=%d", id, err, creates.Load())
	}
}

func TestS3MultipartInitiationRejectsIncompleteDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"missing completeness", ``},
		{"empty truncated page", `<IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextUploadIdMarker>native</NextUploadIdMarker>`},
		{"unexpected prefix", `<IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>key/</Prefix></CommonPrefixes>`},
		{"outside prefix", `<IsTruncated>false</IsTruncated><Upload><Key>other</Key><UploadId>native</UploadId></Upload>`},
		{"duplicate upload", `<IsTruncated>false</IsTruncated>` + strings.Repeat(`<Upload><Key>key</Key><UploadId>native</UploadId></Upload>`, 2)},
		{"excess uploads", `<IsTruncated>false</IsTruncated>` + strings.Repeat(`<Upload><Key>key</Key><UploadId>native</UploadId></Upload>`, api.MaxObjectS3ListItems+1)},
		{"repeated continuation", `<IsTruncated>true</IsTruncated><NextKeyMarker>key-sibling</NextKeyMarker><NextUploadIdMarker>native</NextUploadIdMarker><Upload><Key>key-sibling</Key><UploadId>native</UploadId></Upload>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creates, lists atomic.Int32
			p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				if r.Method == http.MethodPost {
					creates.Add(1)
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>new-upload</UploadId></InitiateMultipartUploadResult>`)
					return
				}
				lists.Add(1)
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult>`+tc.body+`</ListMultipartUploadsResult>`)
			})
			id, err := p.EnsureMultipartUpload(t.Context(), "gregale-test", MultipartCreateRequest{SessionID: "session", Key: "key", SizeBytes: 10})
			if !errors.Is(err, ErrUnavailable) || id != "" || creates.Load() != 0 || lists.Load() > 2 {
				t.Fatalf("unsafe discovery: id=%s err=%v creates=%d lists=%d", id, err, creates.Load(), lists.Load())
			}
		})
	}
}

func TestS3ListingDecodesKeysWithoutDecodingOpaqueToken(t *testing.T) {
	p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("encoding-type") != "url" {
			t.Error("native listing did not request URL-encoded keys")
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListBucketResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextContinuationToken>opaque%2Ftoken+</NextContinuationToken><Contents><Key>folder/a%2Bb%20%E4%B8%96%E7%95%8C</Key><Size>0</Size></Contents></ListBucketResult>`)
	})
	page, err := p.ListObjects(t.Context(), "gregale-test", "folder/", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != "folder/a+b 世界" || page.NextCursor != "opaque%2Ftoken+" {
		t.Fatalf("incorrect decoding: %+v, %v", page, err)
	}
}

func TestS3MultipartDiscoveryPagination(t *testing.T) {
	const key = "folder/a +世界"
	const encodedKey = "folder/a%20%2B%E4%B8%96%E7%95%8C"
	native := strings.Repeat("u", api.ObjectProviderUploadIDMaxBytes)
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("ambiguous=%t", ambiguous), func(t *testing.T) {
			var lists atomic.Int32
			p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Query().Get("prefix") != key || r.URL.Query().Get("encoding-type") != "url" {
					t.Error("unexpected native request", r.Method, r.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/xml")
				if lists.Add(1) == 1 {
					_, _ = fmt.Fprintf(w, `<ListMultipartUploadsResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextUploadIdMarker>%s</NextUploadIdMarker><Upload><Key>%s</Key><UploadId>%s</UploadId></Upload></ListMultipartUploadsResult>`, encodedKey, native, encodedKey, native)
					return
				}
				if r.URL.Query().Get("key-marker") != key || r.URL.Query().Get("upload-id-marker") != native {
					t.Error("native cursor was not preserved")
				}
				_, _ = io.WriteString(w, `<ListMultipartUploadsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
				if ambiguous {
					_, _ = fmt.Fprintf(w, `<Upload><Key>%s</Key><UploadId>other-native</UploadId></Upload>`, encodedKey)
				}
				_, _ = io.WriteString(w, `</ListMultipartUploadsResult>`)
			})
			id, err := p.EnsureMultipartUpload(t.Context(), "gregale-test", MultipartCreateRequest{SessionID: "session", Key: key, SizeBytes: 10})
			if lists.Load() != 2 || ambiguous && !errors.Is(err, ErrConflict) || !ambiguous && (err != nil || id != native) {
				t.Fatalf("incorrect paginated recovery: id=%s err=%v lists=%d", id, err, lists.Load())
			}
		})
	}
}

func TestS3MultipartPartsPagination(t *testing.T) {
	p := listingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("part-number-marker") == "1" {
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker><Part><PartNumber>2</PartNumber><ETag>&quot;part-2&quot;</ETag><Size>10</Size></Part></ListPartsResult>`)
			return
		}
		if r.URL.Query().Get("part-number-marker") != "2" {
			t.Error("incorrect part continuation")
		}
		_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>4</PartNumber><ETag>&quot;part-4&quot;</ETag><Size>10</Size></Part></ListPartsResult>`)
	})
	r := MultipartListPartsRequest{Key: "key", ProviderUploadID: "native", PartNumberMarker: 1, Limit: 1}
	page, err := p.ListMultipartParts(t.Context(), "gregale-test", r)
	if err != nil || len(page.Items) != 1 || page.Items[0].PartNumber != 2 || page.NextPartNumberMarker != 2 {
		t.Fatal(page, err)
	}
	r.PartNumberMarker = page.NextPartNumberMarker
	page, err = p.ListMultipartParts(t.Context(), "gregale-test", r)
	if err != nil || len(page.Items) != 1 || page.Items[0].PartNumber != 4 || page.NextPartNumberMarker != 0 {
		t.Fatal(page, err)
	}
}
