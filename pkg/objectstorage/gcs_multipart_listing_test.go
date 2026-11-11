package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// gcsDocumentedUploadsReply has the shape of the reply in the GCS XML API
// reference ("List multipart uploads"): root ListMultipartUploadsResult in the
// S3 namespace, raw keys and no encoding element. A struct that expected
// another root rejected every real reply, so S3 multipart uploads on GCS
// never started (Hunt 33 H33-41).
func gcsDocumentedUploadsReply(uploads, nextKey, nextUpload string) string {
	return `<?xml version='1.0' encoding='UTF-8'?>` +
		`<ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		`<Bucket>physical</Bucket><KeyMarker></KeyMarker><UploadIdMarker></UploadIdMarker>` +
		`<NextKeyMarker>` + nextKey + `</NextKeyMarker><NextUploadIdMarker>` + nextUpload + `</NextUploadIdMarker>` +
		`<MaxUploads>1000</MaxUploads><IsTruncated>false</IsTruncated>` + uploads +
		`</ListMultipartUploadsResult>`
}

const gcsDocumentedUpload = `<Upload><Key>v1/L0/segment 1.gz</Key><UploadId>VXBsb2FkIElEIGZvciBlbHZpbmcncyBteS1tb3ZpZS5tMnRzIHVwbG9hZA</UploadId>` +
	`<Initiator><ID>initiator-id</ID><DisplayName>initiator</DisplayName></Initiator>` +
	`<Owner><ID>owner-id</ID><DisplayName>owner</DisplayName></Owner>` +
	`<StorageClass>STANDARD</StorageClass><Initiated>2026-11-11T13:30:12.286Z</Initiated></Upload>`

func TestGCSMultipartDiscoveryAcceptsDocumentedReply(t *testing.T) {
	const key, existing = "v1/L0/segment 1.gz", "VXBsb2FkIElEIGZvciBlbHZpbmcncyBteS1tb3ZpZS5tMnRzIHVwbG9hZA"
	for _, tc := range []struct {
		name, reply, want string
		creates           int32
	}{
		{name: "no earlier upload initiates", reply: gcsDocumentedUploadsReply("", "", ""), want: "new-upload", creates: 1},
		{name: "earlier upload is reused", reply: gcsDocumentedUploadsReply(gcsDocumentedUpload, "", ""), want: existing},
		{name: "final page naming its last upload is reused", reply: gcsDocumentedUploadsReply(gcsDocumentedUpload, key, existing), want: existing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creates atomic.Int32
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				if r.Method == http.MethodPost {
					creates.Add(1)
					_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>new-upload</UploadId></InitiateMultipartUploadResult>`)
					return
				}
				if r.URL.Query().Has("encoding-type") || r.URL.Query().Get("prefix") != key {
					t.Error("discovery query", r.URL.RawQuery)
				}
				_, _ = io.WriteString(w, tc.reply)
			}))
			t.Cleanup(native.Close)
			p := testGCS(native.URL, &fakeGCSStore{})
			p.httpClient = native.Client()
			id, err := p.EnsureMultipartUpload(t.Context(), "physical", MultipartCreateRequest{Key: key, SessionID: "session", SizeBytes: 10})
			if err != nil || id != tc.want || creates.Load() != tc.creates {
				t.Fatalf("id=%q err=%v creates=%d, want %q and %d creates", id, err, creates.Load(), tc.want, tc.creates)
			}
		})
	}
}

func TestGCSMultipartDiscoveryRefusesAMarkerPastItsLastUpload(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("inconsistent discovery allowed an initiation")
		}
		_, _ = io.WriteString(w, gcsDocumentedUploadsReply(gcsDocumentedUpload, "v1/L0/segment 1.gz", "another-upload"))
	}))
	t.Cleanup(native.Close)
	p := testGCS(native.URL, &fakeGCSStore{})
	p.httpClient = native.Client()
	if _, err := p.EnsureMultipartUpload(t.Context(), "physical", MultipartCreateRequest{Key: "v1/L0/segment 1.gz", SessionID: "session", SizeBytes: 10}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}
