package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ovhVersionID is the shape OVH returns on every PUT and listing of an
// unversioned bucket (production smoke, 2026-11-21).
const ovhVersionID = "1795265333.741474"

func unversionedTestProvider(t *testing.T, unversioned bool, handler http.Handler) *S3 {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	config := testBackend()
	config.Endpoint = upstream.URL
	if unversioned {
		config.Versioning = BackendVersioningUnsupported
	}
	p, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*S3)
}

// adr: 20261121150000
func TestS3UnversionedBackendDropsWriteVersionIDs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		unversioned bool
		want        string
	}{
		{"provider IDs trusted by default", false, ovhVersionID},
		{"unversioned backend reads the null version", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := unversionedTestProvider(t, tc.unversioned, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("ETag", `"etag"`)
				w.Header().Set("X-Amz-Version-Id", ovhVersionID)
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Length", "1")
					_, _ = io.WriteString(w, "x")
				}
			}))
			out, err := p.WriteObject(t.Context(), "physical", "key", strings.NewReader("x"), 1, ObjectMetadata{ContentType: "text/plain"})
			if err != nil || out.ProviderVersionID != tc.want {
				t.Fatalf("WriteObject = %+v, %v; want version %q", out, err, tc.want)
			}
		})
	}
}

// adr: 20261121150000
func TestS3ResponseTransportStripsVersionHeadersOnlyWhenUnversioned(t *testing.T) {
	for _, unversioned := range []bool{false, true} {
		base := metadataRoundTripper(func(*http.Request) (*http.Response, error) {
			h := http.Header{}
			h.Set("X-Amz-Version-Id", ovhVersionID)
			h.Set("X-Amz-Copy-Source-Version-Id", ovhVersionID)
			h.Set("ETag", `"etag"`)
			return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "https://s3.example.test/b/k", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := s3ResponseTransport{base: base, unversioned: unversioned}.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		kept := response.Header.Get("X-Amz-Version-Id") != "" || response.Header.Get("X-Amz-Copy-Source-Version-Id") != ""
		if kept == unversioned || response.Header.Get("ETag") == "" {
			t.Fatalf("unversioned=%v kept version headers=%v headers=%v", unversioned, kept, response.Header)
		}
	}
}

// adr: 20261121150000
func TestS3UnversionedBackendRefusesVersioning(t *testing.T) {
	var requests atomic.Int32
	p := unversionedTestProvider(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	for _, status := range []string{"Enabled", "Suspended"} {
		if err := p.PutBucketVersioning(t.Context(), "physical", status); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("PutBucketVersioning(%s) = %v, want ErrUnsupported", status, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("an unversioned backend sent %d versioning requests", requests.Load())
	}
}

const ovhVersionListing = `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>` +
	`<Version><Key>probe/hello.txt</Key><VersionId>1795265333.741474</VersionId><IsLatest>true</IsLatest>` +
	`<LastModified>2026-11-21T12:48:53.000Z</LastModified><ETag>"etag"</ETag><Size>25</Size></Version>` +
	`<Version><Key>probe/other.txt</Key><VersionId>1795265334.000001</VersionId><IsLatest>true</IsLatest>` +
	`<LastModified>2026-11-21T12:48:54.000Z</LastModified><ETag>"etag2"</ETag><Size>3</Size></Version></ListVersionsResult>`

func xmlHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, body)
	})
}

// adr: 20261121150000
func TestS3UnversionedBackendListsTheNullVersion(t *testing.T) {
	p := unversionedTestProvider(t, true, xmlHandler(ovhVersionListing))
	inventory, err := p.ListObjectVersions(t.Context(), "physical", "", api.ObjectVersionInventoryPageSize)
	if err != nil || len(inventory.Items) != 2 {
		t.Fatal(inventory, err)
	}
	for _, item := range inventory.Items {
		if item.ProviderVersionID != "null" || item.DeleteMarker {
			t.Fatalf("inventory kept an opaque provider ID: %+v", item)
		}
	}
	listed, err := p.ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Prefix: "probe/", Limit: 10})
	if err != nil || len(listed.Items) != 2 || listed.Items[0].ProviderVersionID != "null" || listed.Items[1].ProviderVersionID != "null" {
		t.Fatal(listed, err)
	}

	// The provider's IDs stay trusted by default.
	trusted := unversionedTestProvider(t, false, xmlHandler(ovhVersionListing))
	inventory, err = trusted.ListObjectVersions(t.Context(), "physical", "", api.ObjectVersionInventoryPageSize)
	if err != nil || len(inventory.Items) != 2 || inventory.Items[0].ProviderVersionID != ovhVersionID {
		t.Fatal(inventory, err)
	}
}

// adr: 20261121150000
func TestS3UnversionedBackendKeepsDeleteMarkerSafetyLatch(t *testing.T) {
	// An unversioned bucket never has a delete marker; one means versioning
	// was enabled outside Gregale, so the history proof must still see it.
	body := strings.Replace(ovhVersionListing, `</ListVersionsResult>`,
		`<DeleteMarker><Key>probe/hello.txt</Key><VersionId>1795265335.000002</VersionId><IsLatest>false</IsLatest><LastModified>2026-11-21T12:49:00.000Z</LastModified></DeleteMarker></ListVersionsResult>`, 1)
	p := unversionedTestProvider(t, true, xmlHandler(body))
	inventory, err := p.ListObjectVersions(t.Context(), "physical", "", api.ObjectVersionInventoryPageSize)
	if err != nil || len(inventory.Items) != 3 || !inventory.Items[2].DeleteMarker || inventory.Items[2].ProviderVersionID != "1795265335.000002" {
		t.Fatal(inventory, err)
	}
}

// adr: 20261121150000
func TestS3UnversionedBackendPaginatesByKeyMarker(t *testing.T) {
	var sawMarker atomic.Bool
	p := unversionedTestProvider(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("version-id-marker") {
			sawMarker.Store(true)
		}
		w.Header().Set("Content-Type", "application/xml")
		if q.Get("key-marker") == "" {
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>probe/hello.txt</NextKeyMarker><NextVersionIdMarker>1795265333.741474</NextVersionIdMarker>`+
				`<Version><Key>probe/hello.txt</Key><VersionId>1795265333.741474</VersionId><IsLatest>true</IsLatest><LastModified>2026-11-21T12:48:53.000Z</LastModified><ETag>"etag"</ETag><Size>25</Size></Version></ListVersionsResult>`)
			return
		}
		if q.Get("key-marker") != "probe/hello.txt" {
			t.Errorf("lost key marker: %v", q)
		}
		_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated>`+
			`<Version><Key>probe/other.txt</Key><VersionId>1795265334.000001</VersionId><IsLatest>true</IsLatest><LastModified>2026-11-21T12:48:54.000Z</LastModified><ETag>"etag2"</ETag><Size>3</Size></Version></ListVersionsResult>`)
	}))
	first, err := p.ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Prefix: "probe/", Limit: 1})
	if err != nil || first.NextKeyMarker != "probe/hello.txt" || first.NextProviderVersionMarker != "null" {
		t.Fatal(first, err)
	}
	second, err := p.ListObjectVersionPage(t.Context(), "physical", ObjectVersionListRequest{Prefix: "probe/", Limit: 1, KeyMarker: first.NextKeyMarker, ProviderVersionMarker: first.NextProviderVersionMarker})
	if err != nil || len(second.Items) != 1 || second.Items[0].Key != "probe/other.txt" {
		t.Fatal(second, err)
	}
	if sawMarker.Load() {
		t.Fatal(`the "null" marker reached the provider`)
	}
}

// adr: 20261121150000
func TestRegistryValidatesBackendVersioning(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*BackendConfig)
		ok   bool
	}{
		{"default", func(*BackendConfig) {}, true},
		{"unsupported on S3", func(b *BackendConfig) { b.Versioning = BackendVersioningUnsupported }, true},
		{"unknown mode", func(b *BackendConfig) { b.Versioning = "disabled" }, false},
		{"unsupported with Object Lock", func(b *BackendConfig) {
			b.Versioning = BackendVersioningUnsupported
			b.ObjectLock.Enabled = true
		}, false},
		{"unsupported on GCS", func(b *BackendConfig) {
			b.Versioning = BackendVersioningUnsupported
			b.Driver = "gcs"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBackend()
			tc.edit(&b)
			factory := func(BackendConfig, func(string) string) (Provider, error) { return nil, errors.New("not built") }
			_, err := NewRegistry(Config{DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}},
				testCredentials, map[string]Factory{"s3": factory, "gcs": factory})
			rejected := err != nil && strings.Contains(err.Error(), "versioning")
			if rejected == tc.ok {
				t.Fatalf("versioning %q (driver %s, lock %v): err = %v", b.Versioning, b.Driver, b.ObjectLock.Enabled, err)
			}
		})
	}
}

// The S3 gateway sends presigned requests with its own client, so their
// responses never pass s3ResponseTransport. NormalizeProviderResponse and
// the write acknowledgment must drop a versionless backend's version IDs
// there too, or every gateway PUT latches recovery_versions_observed (OVH
// production smoke, rc.364, 2026-11-21).
//
// adr: 20261121150000
func TestNormalizeProviderResponseDropsVersionlessVersionIDs(t *testing.T) {
	versioned, err := NewS3(testBackend(), testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	versionlessConfig := testBackend()
	versionlessConfig.Versioning = BackendVersioningUnsupported
	versionless, err := NewS3(versionlessConfig, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		provider    Provider
		wantVersion string
	}{
		{"versionless S3 drops the IDs", versionless, ""},
		{"versioned S3 keeps them", versioned, ovhVersionID},
		{"provider without the capability keeps them", nil, ovhVersionID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{
				"Etag":                         {`"etag"`},
				"X-Amz-Version-Id":             {ovhVersionID},
				"X-Amz-Copy-Source-Version-Id": {ovhVersionID},
			}}
			ack, err := VerifyProviderObjectWriteAcknowledgment(tc.provider, response.Header)
			if err != nil || ack.ProviderVersionID != tc.wantVersion || ack.ETag != `"etag"` {
				t.Fatalf("acknowledgment = %+v, %v; want version %q", ack, err, tc.wantVersion)
			}
			if response.Header.Get("X-Amz-Version-Id") != ovhVersionID {
				t.Fatal("acknowledgment changed the caller's headers")
			}
			NormalizeProviderResponse(tc.provider, response)
			if got := response.Header.Get("X-Amz-Version-Id"); got != tc.wantVersion {
				t.Fatalf("X-Amz-Version-Id = %q, want %q", got, tc.wantVersion)
			}
			if got := response.Header.Get("X-Amz-Copy-Source-Version-Id"); got != tc.wantVersion {
				t.Fatalf("X-Amz-Copy-Source-Version-Id = %q, want %q", got, tc.wantVersion)
			}
		})
	}
	NormalizeProviderResponse(versionless, nil)
	NormalizeProviderResponse(versionless, &http.Response{})
}

// OVH ends every ListParts with NextPartNumberMarker set to the last part it
// returned, truncated or not (production capture, 2026-11-21). Rejecting that
// made ListParts and every gateway CompleteMultipartUpload answer 503.
func TestS3ListPartsAcceptsAFinalPageNamingItsLastPart(t *testing.T) {
	part := func(n string) string {
		return `<Part><PartNumber>` + n + `</PartNumber><LastModified>2026-11-21T20:42:14.000Z</LastModified><ETag>"a24cd6b7281be99a7c4cc025369abe76"</ETag><Size>1048576</Size></Part>`
	}
	for _, tc := range []struct {
		name      string
		marker    int32
		body      string
		wantParts int
		wantErr   bool
	}{
		{"OVH final page names its last part", 0, `<IsTruncated>false</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker>` + part("1") + part("2"), 2, false},
		{"final page without a marker", 0, `<IsTruncated>false</IsTruncated>` + part("1"), 1, false},
		{"final page with marker 0", 0, `<IsTruncated>false</IsTruncated><NextPartNumberMarker>0</NextPartNumberMarker>` + part("1"), 1, false},
		{"empty final page repeats the request marker", 3, `<IsTruncated>false</IsTruncated><NextPartNumberMarker>3</NextPartNumberMarker>`, 0, false},
		{"final page marker past its last part", 0, `<IsTruncated>false</IsTruncated><NextPartNumberMarker>3</NextPartNumberMarker>` + part("1") + part("2"), 0, true},
		{"empty final page marker past the request", 1, `<IsTruncated>false</IsTruncated><NextPartNumberMarker>2</NextPartNumberMarker>`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := unversionedTestProvider(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, `<?xml version='1.0' encoding='UTF-8'?><ListPartsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>physical</Bucket><Key>key</Key><UploadId>upload</UploadId><StorageClass>STANDARD</StorageClass><MaxParts>1000</MaxParts>`+tc.body+`</ListPartsResult>`)
			}))
			page, err := p.ListMultipartParts(t.Context(), "physical", MultipartListPartsRequest{Key: "key", ProviderUploadID: "upload", PartNumberMarker: tc.marker, Limit: 1000})
			if tc.wantErr {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("accepted a final page that claims unreturned parts: %+v, %v", page, err)
				}
				return
			}
			if err != nil || len(page.Items) != tc.wantParts || page.NextPartNumberMarker != 0 {
				t.Fatalf("page = %+v, %v; want %d parts and no continuation", page, err, tc.wantParts)
			}
		})
	}
}
