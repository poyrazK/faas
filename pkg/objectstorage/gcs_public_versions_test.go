package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// adr: 628
func TestGCSVersionListingCapturedNativeEncoding(t *testing.T) {
	body, err := os.ReadFile("testdata/gcs_versions_url.xml")
	if err != nil {
		t.Fatal(err)
	}
	key := "qualification/versioned/世界 +%.txt"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("prefix") != key || r.URL.Query().Get("encoding-type") != "url" || r.Header.Get("X-Goog-Interop-List-Objects-Format") != "enabled" {
			t.Error("wrong native version request")
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	page, err := testGCS(server.URL, &fakeGCSStore{}).ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Prefix: key, Limit: 10})
	if err != nil || len(page.Items) != 2 || page.NextKeyMarker != "" {
		t.Fatal(page, err)
	}
	for i, id := range []string{"1791390110617942", "1791390111175567"} {
		if page.Items[i].Key != key || page.Items[i].ProviderVersionID != id || page.Items[i].IsLatest != (i == 1) {
			t.Fatal("native generation identity changed", page.Items[i])
		}
	}
}

// adr: 628
func TestGCSVersionListingEncodingAndPagination(t *testing.T) {
	for _, tag := range []string{"Encoding-Type", "EncodingType"} {
		t.Run(tag, func(t *testing.T) {
			key, prefix := "dir/世界 +%.txt", "dir/目录 +%/"
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					_, _ = io.WriteString(w, `<ListVersionsResult><`+tag+`>url</`+tag+`><IsTruncated>true</IsTruncated><Version><Key>dir/%E4%B8%96%E7%95%8C+%2B%25.txt</Key><VersionId>12</VersionId><Size>7</Size><ETag>&quot;etag&quot;</ETag><IsLatest>true</IsLatest><LastModified>2026-10-06T12:00:00Z</LastModified></Version><CommonPrefixes><Prefix>dir/%E7%9B%AE%E5%BD%95%20%2B%25/</Prefix></CommonPrefixes><NextKeyMarker>dir/%E4%B8%96%E7%95%8C+%2B%25.txt</NextKeyMarker><NextVersionIdMarker>12</NextVersionIdMarker></ListVersionsResult>`)
					return
				}
				if r.URL.Query().Get("key-marker") != key || r.URL.Query().Get("version-id-marker") != "12" || r.URL.Query().Get("prefix") != "dir/" || r.URL.Query().Get("delimiter") != "/" {
					t.Error("decoded continuation was not preserved")
				}
				_, _ = io.WriteString(w, `<ListVersionsResult><`+tag+`>url</`+tag+`><IsTruncated>false</IsTruncated></ListVersionsResult>`)
			}))
			defer server.Close()
			p := testGCS(server.URL, &fakeGCSStore{})
			request := ObjectVersionListRequest{Prefix: "dir/", Delimiter: "/", Limit: 2}
			page, err := p.ListObjectVersionPage(t.Context(), "physical", request)
			if err != nil || len(page.Items) != 1 || page.Items[0].Key != key || len(page.CommonPrefixes) != 1 || page.CommonPrefixes[0] != prefix || page.NextKeyMarker != key || page.NextProviderVersionMarker != "12" {
				t.Fatal(page, err)
			}
			request.KeyMarker, request.ProviderVersionMarker = page.NextKeyMarker, page.NextProviderVersionMarker
			if _, err := p.ListObjectVersionPage(t.Context(), "physical", request); err != nil || requests != 2 {
				t.Fatal("continuation failed", err, requests)
			}
		})
	}
}

// adr: 628
func TestGCSVersionListingRejectsAmbiguousEncoding(t *testing.T) {
	for name, encoding := range map[string]string{
		"unsupported native":  `<Encoding-Type>base64</Encoding-Type>`,
		"unsupported legacy":  `<EncodingType>base64</EncodingType>`,
		"empty native":        `<Encoding-Type/>`,
		"conflicting aliases": `<Encoding-Type>url</Encoding-Type><EncodingType>base64</EncodingType>`,
		"repeated native":     `<Encoding-Type>url</Encoding-Type><Encoding-Type>url</Encoding-Type>`,
		"repeated legacy":     `<EncodingType>url</EncodingType><EncodingType>url</EncodingType>`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `<ListVersionsResult>`+encoding+`<IsTruncated>false</IsTruncated></ListVersionsResult>`)
			}))
			defer server.Close()
			if _, err := testGCS(server.URL, &fakeGCSStore{}).ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Limit: 1}); !errors.Is(err, ErrUnavailable) {
				t.Fatal("ambiguous encoding accepted", err)
			}
		})
	}
}

// adr: 628
func TestGCSVersionListingRawAndMalformedKeys(t *testing.T) {
	for _, tc := range []struct {
		name, encoding, key, want string
	}{
		{name: "unencoded literal plus", key: "dir/世界+%.txt", want: "dir/世界+%.txt"},
		{name: "invalid percent escape", encoding: `<Encoding-Type>url</Encoding-Type>`, key: "dir/%ZZ"},
		{name: "invalid decoded UTF8", encoding: `<Encoding-Type>url</Encoding-Type>`, key: "dir/%FF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `<ListVersionsResult>`+tc.encoding+`<IsTruncated>false</IsTruncated><Version><Key>`+tc.key+`</Key><VersionId>12</VersionId><Size>7</Size><ETag>&quot;etag&quot;</ETag><IsLatest>true</IsLatest><LastModified>2026-10-06T12:00:00Z</LastModified></Version></ListVersionsResult>`)
			}))
			defer server.Close()
			page, err := testGCS(server.URL, &fakeGCSStore{}).ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Limit: 1})
			if tc.want == "" {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("malformed key accepted", page, err)
				}
			} else if err != nil || len(page.Items) != 1 || page.Items[0].Key != tc.want {
				t.Fatal(page, err)
			}
		})
	}
}

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
