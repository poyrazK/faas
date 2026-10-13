package s3gateway

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1038
func TestObjectAttributesRequestValidation(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://s3.example.test/b/k?attributes", nil)
	for _, a := range []string{"ETag", "Checksum", "ObjectParts", "ObjectSize"} {
		r.Header.Add("X-Amz-Object-Attributes", a)
	}
	in, err := objectAttributesRequest(r, "key")
	if err != nil || len(in.Attributes) != 4 {
		t.Fatal("standard repeated SDK header rejected", in, err)
	}
	for _, tc := range []struct{ name, key, value string }{{"duplicate selection", "X-Amz-Object-Attributes", "Checksum,Checksum"}, {"unknown selection", "X-Amz-Object-Attributes", "ACL"}, {"oversized list", "X-Amz-Object-Attributes", strings.Repeat("ETag,", 50)}, {"zero page", "X-Amz-Max-Parts", "0"}, {"large page", "X-Amz-Max-Parts", "1001"}, {"negative marker", "X-Amz-Part-Number-Marker", "-1"}, {"large marker", "X-Amz-Part-Number-Marker", "10001"}, {"ignored condition", "If-Match", "anything"}} {
		t.Run(tc.name, func(t *testing.T) {
			copy := r.Clone(t.Context())
			copy.Header.Set(tc.key, tc.value)
			if _, err := objectAttributesRequest(copy, "key"); err == nil {
				t.Fatal("invalid attribute request admitted")
			}
		})
	}
}

// adr: 1038
func TestObjectAttributesDeleteMarkerPublicIdentity(t *testing.T) {
	nativeID := "private-attribute-marker"
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("X-Amz-Version-Id", nativeID)
		w.Header().Set("X-Amz-Delete-Marker", "true")
		w.Header().Set("Last-Modified", "Tue, 13 Oct 2026 10:00:00 GMT")
		status := http.StatusNotFound
		if r.URL.Query().Get("versionId") != "" {
			if r.URL.Query().Get("versionId") != nativeID {
				t.Error("public identity forwarded to native provider")
			}
			status = http.StatusMethodNotAllowed
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `<Error><Code>MethodNotAllowed</Code></Error>`)
	}))
	input := &awss3.GetObjectAttributesInput{Bucket: aws.String("assets"), Key: aws.String("deleted"), ObjectAttributes: []types.ObjectAttributes{types.ObjectAttributesEtag}}
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		_, err := f.client.GetObjectAttributes(t.Context(), input)
		var response *smithyhttp.ResponseError
		if !errors.As(err, &response) || response.HTTPStatusCode() != status {
			t.Fatal(err)
		}
		header := response.Response.Header
		id := header.Get("X-Amz-Version-Id")
		if id == nativeID || !state.ValidObjectVersionID(id) || header.Get("X-Amz-Delete-Marker") != "true" || status == http.StatusMethodNotAllowed && header.Get("Last-Modified") == "" {
			t.Fatal("marker identity or metadata lost", header)
		}
		if input.VersionId != nil && id != *input.VersionId {
			t.Fatal("marker identity changed between reads")
		}
		input.VersionId = aws.String(id)
	}
}

func TestObjectAttributeChecksumHeaderValidation(t *testing.T) {
	for _, tc := range []struct {
		kind, value     string
		duplicate, want bool
	}{{objectchecksum.FullObject, "AAAAAA==", false, true}, {objectchecksum.Composite, "AAAAAA==-2", false, true}, {objectchecksum.Composite, "AAAAAA==-0", false, false}, {objectchecksum.Composite, "AAAAAA==", false, false}, {objectchecksum.FullObject, "wrong", false, false}, {objectchecksum.FullObject, "AAAAAA==", true, false}, {"UNKNOWN", "AAAAAA==", false, false}} {
		h := http.Header{}
		h.Set("X-Amz-Checksum-Type", tc.kind)
		h.Add("X-Amz-Checksum-Crc32c", tc.value)
		if tc.duplicate {
			h.Add("X-Amz-Checksum-Crc32c", tc.value)
		}
		if got := validReadChecksumHeaders(h); got != tc.want {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}

func TestMultipartChecksumXMLRejectsAmbiguousPart(t *testing.T) {
	for _, body := range []string{`<Part><PartNumber>1</PartNumber><PartNumber>2</PartNumber><ETag>"a"</ETag></Part>`, `<Part><PartNumber>1</PartNumber><ETag>"a"</ETag><ChecksumCRC32>AAAAAA==</ChecksumCRC32><ChecksumCRC32>AAAAAA==</ChecksumCRC32></Part>`, `<Part><PartNumber>1</PartNumber><ETag>"a"</ETag><ChecksumMD5>AAAAAA==</ChecksumMD5></Part>`} {
		var part completeMultipartPart
		if err := xml.Unmarshal([]byte(body), &part); err == nil {
			t.Fatal("ambiguous part accepted", part)
		}
	}
}
