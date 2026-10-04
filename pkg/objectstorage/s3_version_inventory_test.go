package objectstorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 540
func TestS3VersionInventoryPagination(t *testing.T) {
	key := "目录 /+%.txt"
	var requests atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Has("prefix") || !q.Has("versions") || q.Get("encoding-type") != "url" || q.Get("max-keys") != "1000" {
			t.Error("wrong version inventory", r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		if q.Get("key-marker") == "" {
			_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>marker/+%%</NextVersionIdMarker><Version><Key>%s</Key><VersionId>null</VersionId><Size>3</Size></Version><Version><Key>%s</Key><VersionId>v1</VersionId><Size>4</Size></Version><DeleteMarker><Key>%s</Key><VersionId>marker/+%%</VersionId></DeleteMarker></ListVersionsResult>`, url.PathEscape(key), url.PathEscape(key), url.PathEscape(key), url.PathEscape(key))
		} else {
			if q.Get("key-marker") != key || q.Get("version-id-marker") != "marker/+%" {
				t.Error("lost paired cursor", q)
			}
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>other</Key><VersionId>v2</VersionId><Size>5</Size></Version></ListVersionsResult>`)
		}
	})).(ObjectVersionInventoryProvider)
	page, err := p.ListObjectVersions(t.Context(), "bucket", "", api.ObjectVersionInventoryPageSize)
	if err != nil || len(page.Items) != 3 || page.NextCursor == "" || page.Items[2].SizeBytes != int64(len(key)) || !page.Items[2].DeleteMarker || page.Items[0].ProviderVersionID != "null" {
		t.Fatal(page, err)
	}
	raw, err := json.Marshal(page)
	if err != nil || strings.Contains(string(raw), "marker/+%") || strings.Contains(string(raw), page.NextCursor) {
		t.Fatal("private native inventory serialized", string(raw), err)
	}
	if _, err = p.ListObjectVersions(t.Context(), "another", page.NextCursor, 1000); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor crossed buckets", err)
	}
	page, err = p.ListObjectVersions(t.Context(), "bucket", page.NextCursor, 1000)
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" || requests.Load() != 2 {
		t.Fatal(page, err, requests.Load())
	}
}

func TestS3VersionInventoryRejectsPartialOrMalformedPages(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"missing truncation", `<ListVersionsResult/>`, 200, ErrUnavailable},
		{"missing size", `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>v</VersionId></Version></ListVersionsResult>`, 200, ErrUnavailable},
		{"negative size", `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>v</VersionId><Size>-1</Size></Version></ListVersionsResult>`, 200, ErrUnavailable},
		{"missing version", `<ListVersionsResult><IsTruncated>false</IsTruncated><DeleteMarker><Key>key</Key></DeleteMarker></ListVersionsResult>`, 200, ErrUnavailable},
		{"duplicate identity", `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>v</VersionId><Size>1</Size></Version><DeleteMarker><Key>key</Key><VersionId>v</VersionId></DeleteMarker></ListVersionsResult>`, 200, ErrUnavailable},
		{"invalid key encoding", `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated><Version><Key>%ZZ</Key><VersionId>v</VersionId><Size>1</Size></Version></ListVersionsResult>`, 200, ErrUnavailable},
		{"missing continuation", `<ListVersionsResult><IsTruncated>true</IsTruncated><Version><Key>key</Key><VersionId>v</VersionId><Size>1</Size></Version></ListVersionsResult>`, 200, ErrUnavailable},
		{"empty truncated", `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>v</NextVersionIdMarker></ListVersionsResult>`, 200, ErrUnavailable},
		{"delimiter results", `<ListVersionsResult><IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>key/</Prefix></CommonPrefixes></ListVersionsResult>`, 200, ErrUnavailable},
		{"unsupported", `<Error><Code>NotImplemented</Code><Message>private provider</Message></Error>`, 501, ErrUnsupported},
		{"denied", `<Error><Code>AccessDenied</Code><Message>private provider</Message></Error>`, 403, ErrConfiguration},
		{"server failure", `<Error><Code>InternalError</Code><Message>private provider</Message></Error>`, 500, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})).(ObjectVersionInventoryProvider)
			if _, err := p.ListObjectVersions(t.Context(), "bucket", "", 1000); !errors.Is(err, tc.want) || requests.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatal(err, requests.Load())
			}
		})
	}
}

func TestS3VersionInventoryInputLimits(t *testing.T) {
	var requests atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) })).(ObjectVersionInventoryProvider)
	for _, tc := range []struct {
		cursor string
		limit  int32
	}{{"", 0}, {"", 1001}, {"invalid", 1000}, {strings.Repeat("x", 8193), 1000}} {
		if _, err := p.ListObjectVersions(t.Context(), "bucket", tc.cursor, tc.limit); !errors.Is(err, ErrInvalid) || requests.Load() != 0 {
			t.Fatal(err, requests.Load())
		}
	}
}
