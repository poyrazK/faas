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
)

// adr: 542
func TestS3PublicVersionsPairedPagination(t *testing.T) {
	key, version := "目录 /+%.txt", "native/+%version"
	var calls atomic.Int32
	lister := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if !q.Has("versions") || q.Get("encoding-type") != "url" || q.Get("max-keys") != "3" || q.Get("prefix") != "目录 " || q.Get("delimiter") != "/" {
			t.Error("wrong list arguments", q)
		}
		if q.Get("key-marker") != "" && (q.Get("key-marker") != key || q.Get("version-id-marker") != version) {
			t.Error("private paired cursor changed", q)
		}
		w.Header().Set("Content-Type", "application/xml")
		if q.Get("key-marker") != "" {
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
			return
		}
		_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>%s</NextVersionIdMarker><Version><Key>%s</Key><VersionId>null</VersionId><Size>3</Size><StorageClass>GLACIER</StorageClass><ETag>&quot;etag&quot;</ETag><IsLatest>false</IsLatest><LastModified>2026-10-02T10:00:00Z</LastModified></Version><DeleteMarker><Key>%s</Key><VersionId>private-marker</VersionId><IsLatest>true</IsLatest><LastModified>2026-10-02T11:00:00Z</LastModified></DeleteMarker><CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes></ListVersionsResult>`, url.PathEscape(key), version, url.PathEscape(key), url.PathEscape(key), url.PathEscape("目录 folder/"))
	})).(ObjectVersionLister)
	input := ObjectVersionListRequest{Prefix: "目录 ", Delimiter: "/", Limit: 3}
	page, err := lister.ListObjectVersionPage(t.Context(), "bucket", input)
	if err != nil || len(page.Items) != 2 || len(page.CommonPrefixes) != 1 || page.Items[0].Key != key || page.Items[0].StorageClass != "GLACIER" || !page.Items[1].DeleteMarker || !page.Items[1].IsLatest || page.NextKeyMarker != key || page.NextProviderVersionMarker != version {
		t.Fatal(page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), version) || strings.Contains(string(raw), "private-marker") {
		t.Fatal("native list identities serialized", string(raw))
	}
	input.KeyMarker, input.ProviderVersionMarker = page.NextKeyMarker, page.NextProviderVersionMarker
	page, err = lister.ListObjectVersionPage(t.Context(), "bucket", input)
	if err != nil || len(page.Items) != 0 || page.NextKeyMarker != "" || calls.Load() != 2 {
		t.Fatal(page, err, calls.Load())
	}
}

func TestS3PublicVersionListingRejectsMalformedProviderResponses(t *testing.T) {
	valid := `<Version><Key>key</Key><VersionId>native</VersionId><Size>3</Size><ETag>&quot;etag&quot;</ETag><IsLatest>true</IsLatest><LastModified>2026-10-02T10:00:00Z</LastModified></Version>`
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"missing latest", strings.ReplaceAll(valid, "<IsLatest>true</IsLatest>", ""), 200, ErrUnavailable},
		{"missing modified", strings.ReplaceAll(valid, "<LastModified>2026-10-02T10:00:00Z</LastModified>", ""), 200, ErrUnavailable},
		{"missing etag", strings.ReplaceAll(valid, "<ETag>&quot;etag&quot;</ETag>", ""), 200, ErrUnavailable},
		{"missing size", strings.ReplaceAll(valid, "<Size>3</Size>", ""), 200, ErrUnavailable},
		{"negative size", strings.ReplaceAll(valid, "<Size>3</Size>", "<Size>-1</Size>"), 200, ErrUnavailable},
		{"missing native", strings.ReplaceAll(valid, "<VersionId>native</VersionId>", ""), 200, ErrUnavailable},
		{"duplicate identity", valid + valid, 200, ErrUnavailable},
		{"stray continuation", valid + "<NextKeyMarker>key</NextKeyMarker>", 200, ErrUnavailable},
		{"missing continuation", strings.ReplaceAll(valid, "<IsLatest>true</IsLatest>", "<IsLatest>true</IsLatest>") + "<IsTruncated>true</IsTruncated>", 200, ErrUnavailable},
		{"unexpected prefixes", `<CommonPrefixes><Prefix>folder/</Prefix></CommonPrefixes>`, 200, ErrUnavailable},
		{"invalid XML", `<Version>`, 200, ErrUnavailable},
		{"failure is one request", `<Error><Code>InternalError</Code><Message>provider-private</Message></Error>`, 500, ErrUnavailable},
		{"unsupported", `<Error><Code>NotImplemented</Code></Error>`, 501, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					_, _ = io.WriteString(w, "<ListVersionsResult><IsTruncated>false</IsTruncated>"+tc.body+"</ListVersionsResult>")
				} else {
					_, _ = io.WriteString(w, tc.body)
				}
			})).(ObjectVersionLister)
			if _, err := p.ListObjectVersionPage(t.Context(), "bucket", ObjectVersionListRequest{Limit: 1}); !errors.Is(err, tc.want) || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
}

func TestS3PublicVersionInputValidation(t *testing.T) {
	var calls atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) })).(*S3)
	for _, input := range []ObjectVersionListRequest{{Limit: 0}, {Limit: 1001}, {Limit: 1, Prefix: "\xff"}, {Limit: 1, KeyMarker: "bad\n"}, {Limit: 1, Delimiter: "ab"}, {Limit: 1, ProviderVersionMarker: "native"}, {Limit: 1, KeyMarker: "key", ProviderVersionMarker: "bad\n"}} {
		if _, err := p.ListObjectVersionPage(t.Context(), "bucket", input); !errors.Is(err, ErrInvalid) || calls.Load() != 0 {
			t.Fatal(input, err, calls.Load())
		}
	}
	for _, tc := range []struct {
		method, key, native string
		ttl                 int64
	}{{"DELETE", "key", "native", 60}, {"GET", "", "native", 60}, {"GET", "key", "", 60}, {"HEAD", "key", "bad\n", 60}, {"GET", "key", "native", -1}} {
		if _, err := p.PresignVersionRead(t.Context(), "bucket", tc.method, tc.key, tc.native, false, tc.ttl); !errors.Is(err, ErrInvalid) || calls.Load() != 0 {
			t.Fatal(tc, err, calls.Load())
		}
	}
}

func TestS3PresignedVersionReadBindsExactKeyAndChecksum(t *testing.T) {
	p := historyTestProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("presigning made an outbound request") })).(VersionReadPresigner)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, checksum := range []bool{false, true} {
			out, err := p.PresignVersionRead(t.Context(), "bucket", method, "目录 /+%.txt", "native/+%?id", checksum, 60)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(out.URL)
			if err != nil || u.Path != "/bucket/目录 /+%.txt" || u.Query().Get("versionId") != "native/+%?id" || u.Query().Get("X-Amz-Expires") != "60" || out.Method != method {
				t.Fatal(out, err)
			}
			mode := u.Query().Get("X-Amz-Checksum-Mode")
			for name, value := range out.Headers {
				if strings.EqualFold(name, "x-amz-checksum-mode") {
					mode = value
				}
			}
			if checksum && mode != "ENABLED" || !checksum && mode != "" || u.Query().Get("response-content-disposition") != "" {
				t.Fatal("version read lost checksum or metadata", out)
			}
		}
	}
}
