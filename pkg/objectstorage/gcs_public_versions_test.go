package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// adr: 628
func TestGCSVersionsNativeInteropAndSelectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("versions") || r.URL.Query().Get("encoding-type") != "url" || r.URL.Query().Get("version-id-marker") != "11" || r.Header.Get("X-Goog-Interop-List-Objects-Format") != "enabled" {
			t.Error("wrong interoperable list request")
		}
		_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><Version><Key>dir%2F%2B%25</Key><VersionId>12</VersionId><Size>7</Size><ETag>&quot;etag&quot;</ETag><IsLatest>true</IsLatest><LastModified>2026-10-06T12:00:00Z</LastModified></Version><NextKeyMarker>dir%2F%2B%25</NextKeyMarker><NextVersionIdMarker>12</NextVersionIdMarker></ListVersionsResult>`)
	}))
	defer server.Close()
	p := testGCS(server.URL, &fakeGCSStore{})
	page, err := p.ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{KeyMarker: "dir/+%", ProviderVersionMarker: "11", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != "dir/+%" || page.NextProviderVersionMarker != "12" || page.Items[0].DeleteMarker {
		t.Fatal(page, err)
	}
	signed, err := p.PresignVersionRead(t.Context(), "physical", "HEAD", "dir/+%", "12", false, 60)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(signed.URL)
	if u.Query().Get("generation") != "12" || u.Query().Has("versionId") || strings.Contains(u.RawQuery, "response-content-") {
		t.Fatal("selector or metadata override wrong")
	}
}

// adr: 628
func TestGCSVersionListingRejectsIncompleteProviderRows(t *testing.T) {
	valid := `<Version><Key>key</Key><VersionId>12</VersionId><Size>7</Size><ETag>&quot;etag&quot;</ETag><IsLatest>true</IsLatest><LastModified>2026-10-06T12:00:00Z</LastModified></Version>`
	for name, body := range map[string]string{"size absent": strings.ReplaceAll(valid, "<Size>7</Size>", ""), "generation invalid": strings.ReplaceAll(valid, "<VersionId>12</VersionId>", "<VersionId>012</VersionId>"), "duplicate": valid + valid, "unexpected marker": `<DeleteMarker/>`, "stray cursor": valid + `<NextKeyMarker>key</NextKeyMarker>`} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated>`+body+`</ListVersionsResult>`)
			}))
			defer server.Close()
			p := testGCS(server.URL, &fakeGCSStore{})
			if _, err := p.ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Limit: 2}); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

// adr: 628
func TestGCSReadProofRejectsAmbiguousNativeIdentities(t *testing.T) {
	p := testGCS(gcsDefaultEndpoint, &fakeGCSStore{})
	for name, headers := range map[string]http.Header{
		"absent":         {},
		"noncanonical":   {"X-Goog-Generation": {"012"}},
		"duplicate":      {"X-Goog-Generation": {"12", "13"}},
		"foreign header": {"X-Goog-Generation": {"12"}, "X-Amz-Version-Id": {"private-native"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ProviderReadVersionHeader(p, &http.Response{StatusCode: 200, Header: headers}); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
	if generation, err := ProviderReadVersionHeader(p, &http.Response{StatusCode: 200, Header: http.Header{"X-Goog-Generation": {"12"}}}); err != nil || generation != "12" {
		t.Fatal(generation, err)
	}
	for name, headers := range map[string]http.Header{
		"unknown CMEK": {"X-Goog-Encryption-Kms-Key-Name": {"private-key"}},
		"customer key": {"X-Goog-Encryption-Key-Sha256": {"private-hash"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ProviderReadEncryption(p, EncryptionConfig{}, "", headers); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
	if out, err := ProviderReadEncryption(p, EncryptionConfig{}, "", http.Header{}); err != nil || out.Algorithm != "AES256" {
		t.Fatal(out, err)
	}
}
