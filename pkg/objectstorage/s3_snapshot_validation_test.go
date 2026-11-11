package objectstorage

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestS3SnapshotListingRequiresCompleteEvidence(t *testing.T) {
	version := `<Version><Key>key</Key><VersionId>v1</VersionId><IsLatest>true</IsLatest><LastModified>2026-09-01T00:00:00Z</LastModified><Size>0</Size><ETag>&quot;empty&quot;</ETag></Version>`
	markers := `<NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>v1</NextVersionIdMarker>`
	for _, tc := range []struct {
		name, body string
		wantError  bool
		items      int
	}{
		{"confirmed empty", `<IsTruncated>false</IsTruncated>`, false, 0},
		{"confirmed zero-byte object", `<IsTruncated>false</IsTruncated>` + version, false, 1},
		{"missing completion flag", "", true, 0},
		{"missing flag with continuation", markers, true, 0},
		{"partial data without flag", version + markers, true, 0},
		{"contradictory completion", `<IsTruncated>false</IsTruncated>` + version + markers, true, 0},
		{"missing continuation", `<IsTruncated>true</IsTruncated>` + version, true, 0},
		{"empty truncated page", `<IsTruncated>true</IsTruncated>` + markers, true, 0},
		{"missing size", `<IsTruncated>false</IsTruncated>` + strings.ReplaceAll(version, `<Size>0</Size>`, ""), true, 0},
		{"missing timestamp", `<IsTruncated>false</IsTruncated>` + strings.ReplaceAll(version, `<LastModified>2026-09-01T00:00:00Z</LastModified>`, ""), true, 0},
		{"duplicate version", `<IsTruncated>false</IsTruncated>` + version + version, true, 0},
		{"unexpected delimiter", `<IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>other/</Prefix></CommonPrefixes>`, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("versioning") {
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
					return
				}
				_, _ = io.WriteString(w, `<ListVersionsResult>`+tc.body+`</ListVersionsResult>`)
			}))
			t.Cleanup(native.Close)
			config := testBackend()
			config.Endpoint = native.URL
			provider, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := CaptureObjectManifest(t.Context(), provider.(VersionedObjectLister), "source", time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), 2)
			if tc.wantError {
				if !errors.Is(err, ErrUnavailable) || manifest != nil {
					t.Fatalf("incomplete evidence accepted: manifest=%+v error=%v", manifest, err)
				}
			} else if err != nil || len(manifest) != tc.items {
				t.Fatalf("complete listing refused: manifest=%+v error=%v", manifest, err)
			}
		})
	}
}

func TestS3SnapshotVersionCursorCannotRepeat(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>v1</NextVersionIdMarker><Version><Key>key</Key><VersionId>v1</VersionId><IsLatest>true</IsLatest><LastModified>2026-09-01T00:00:00Z</LastModified><Size>0</Size><ETag>&quot;empty&quot;</ETag></Version></ListVersionsResult>`)
	}))
	defer native.Close()
	config := testBackend()
	config.Endpoint = native.URL
	provider, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	cursor := base64.RawURLEncoding.EncodeToString([]byte(`{"k":"key","v":"v1"}`))
	if _, err := provider.(VersionedObjectLister).ListSnapshotObjectVersions(t.Context(), "source", cursor, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatal("repeated continuation accepted", err)
	}
}
