package s3gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/state"
)

type integrityNativePart struct {
	value, etag string
	data        []byte
}
type integrityNativeFixture struct {
	mu                               sync.Mutex
	policy                           objectchecksum.Policy
	parts                            map[int32]integrityNativePart
	metadata                         map[string]string
	result                           objectchecksum.Snapshot
	body                             []byte
	completed, loseAck, malformedAck bool
	completions                      int
}

// The native fixture independently hashes received bytes, binds the initiation
// policy, verifies completion checksums, and retains them after the upload ends.
func (p *integrityNativeFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	if r.Method == http.MethodPost && q.Has("uploads") {
		var err error
		p.policy, err = objectchecksum.NewPolicy(r.Header.Get("X-Amz-Checksum-Algorithm"), r.Header.Get("X-Amz-Checksum-Type"))
		if err != nil || p.policy.Empty() {
			t.Errorf("native policy was lost: %v %v", p.policy, err)
			w.WriteHeader(400)
			return
		}
		p.parts = map[int32]integrityNativePart{}
		p.metadata = map[string]string{}
		for name, values := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
				p.metadata[strings.TrimPrefix(strings.ToLower(name), "x-amz-meta-")] = values[0]
			}
		}
		_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>private-integrity-upload</UploadId></InitiateMultipartUploadResult>`)
		return
	}
	if q.Get("uploadId") != "" {
		if p.completed {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		switch r.Method {
		case http.MethodPut:
			n, _ := strconv.ParseInt(q.Get("partNumber"), 10, 32)
			b, err := io.ReadAll(io.LimitReader(r.Body, api.MinMultipartPartBytes+1024))
			if err != nil {
				t.Fatal(err)
			}
			h := objectchecksum.NewHash(p.policy.Algorithm)
			_, _ = h.Write(b)
			value := base64.StdEncoding.EncodeToString(h.Sum(nil))
			declared := r.Header.Get(checksumHeader(p.policy.Algorithm))
			if declared == "" {
				for name, values := range q {
					if strings.EqualFold(name, checksumHeader(p.policy.Algorithm)) {
						declared = values[0]
					}
				}
			}
			if declared != value {
				t.Errorf("native part not bound to its verified checksum: %q != %q", declared, value)
				w.WriteHeader(400)
				return
			}
			etag := fmt.Sprintf(`"part-%d"`, n)
			p.parts[int32(n)] = integrityNativePart{value: value, etag: etag, data: b}
			w.Header().Set("ETag", etag)
			w.Header().Set(checksumHeader(p.policy.Algorithm), value)
			w.WriteHeader(200)
		case http.MethodGet:
			_, _ = fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><ChecksumAlgorithm>%s</ChecksumAlgorithm><ChecksumType>%s</ChecksumType>`, p.policy.Algorithm, p.policy.Type)
			var keys []int
			for n := range p.parts {
				keys = append(keys, int(n))
			}
			sort.Ints(keys)
			for _, n := range keys {
				item := p.parts[int32(n)]
				part := listedMultipartPart{checksumXML: checksumXMLFor(objectchecksum.Digest{Policy: p.policy, Value: item.value}), LastModified: "2026-10-13T10:00:00Z", PartNumber: int32(n), ETag: item.etag, Size: int64(len(item.data))}
				b, _ := xml.Marshal(part)
				b = bytes.ReplaceAll(b, []byte("listedMultipartPart"), []byte("Part"))
				_, _ = w.Write(b)
			}
			_, _ = io.WriteString(w, `</ListPartsResult>`)
		case http.MethodPost:
			var input completeMultipartUploadRequest
			if err := xml.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			var selected []objectchecksum.Part
			p.body = nil
			for _, part := range input.Parts {
				native := p.parts[part.PartNumber]
				value, err := part.checksumXML.selected(p.policy)
				if err != nil || value != "" && value != native.value || native.etag != part.ETag {
					t.Fatalf("native completion changed selected parts: %+v, %+v", part, native)
				}
				selected = append(selected, objectchecksum.Part{Number: part.PartNumber, Size: int64(len(native.data)), Value: native.value})
				p.body = append(p.body, native.data...)
			}
			var err error
			p.result, err = objectchecksum.Build(p.policy, selected)
			if err != nil {
				t.Fatal(err)
			}
			if r.Header.Get(checksumHeader(p.policy.Algorithm)) != p.result.Value || r.Header.Get("X-Amz-Checksum-Type") != p.policy.Type || r.Header.Get("X-Amz-Mp-Object-Size") != strconv.Itoa(len(p.body)) {
				t.Errorf("native completion did not carry the pinned checksum: %v", r.Header)
				w.WriteHeader(400)
				return
			}
			p.completions++
			p.completed = true
			if p.loseAck {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
				return
			}
			w.Header().Set("X-Amz-Version-Id", "private-integrity-version")
			d := p.result.Digest
			if p.malformedAck {
				d.Value = "AAAAAA=="
			}
			b, _ := xml.Marshal(completeMultipartResult{XMLNS: s3XMLNamespace, ETag: `"integrity-object"`, checksumXML: checksumXMLFor(d), ChecksumType: p.policy.Type})
			_, _ = w.Write(b)
		default:
			w.WriteHeader(500)
		}
		return
	}
	if !p.completed {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
		return
	}
	w.Header().Set("X-Amz-Version-Id", "private-integrity-version")
	w.Header().Set("ETag", `"integrity-object"`)
	w.Header().Set("Last-Modified", "Tue, 13 Oct 2026 10:00:00 GMT")
	if q.Has("attributes") {
		marker, _ := strconv.ParseInt(r.Header.Get("X-Amz-Part-Number-Marker"), 10, 32)
		limit, _ := strconv.ParseInt(r.Header.Get("X-Amz-Max-Parts"), 10, 32)
		if limit == 0 {
			limit = 1000
		}
		result := objectAttributesXML{XMLNS: s3XMLNamespace, ETag: "integrity-object", Size: aws.Int64(int64(len(p.body))), StorageClass: "STANDARD", Checksum: &attributeChecksumXML{checksumXML: checksumXMLFor(p.result.Digest), Type: p.policy.Type}}
		page := &attributePartsXML{Count: int32(len(p.result.Parts)), Marker: int32(marker), Max: int32(limit)}
		for _, part := range p.result.Parts {
			if part.Number > int32(marker) && len(page.Parts) < int(limit) {
				page.Parts = append(page.Parts, attributePartXML{checksumXML: checksumXMLFor(objectchecksum.Digest{Policy: p.policy, Value: part.Value}), Number: part.Number, Size: part.Size})
			}
		}
		last := int32(marker)
		if len(page.Parts) > 0 {
			last = page.Parts[len(page.Parts)-1].Number
		}
		page.Truncated = last < page.Count
		if page.Truncated {
			page.Next = last
		}
		result.Parts = page
		b, _ := xml.Marshal(result)
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(p.body)))
	for key, value := range p.metadata {
		w.Header().Set("X-Amz-Meta-"+key, value)
	}
	writeChecksumHeaders(w.Header(), p.result.Digest, true)
	if r.Method != http.MethodHead {
		_, _ = w.Write(p.body)
	}
}

func setSDKPartChecksum(p *types.CompletedPart, algorithm, value string) {
	switch algorithm {
	case "CRC32":
		p.ChecksumCRC32 = aws.String(value)
	case "CRC32C":
		p.ChecksumCRC32C = aws.String(value)
	case "CRC64NVME":
		p.ChecksumCRC64NVME = aws.String(value)
	case "SHA1":
		p.ChecksumSHA1 = aws.String(value)
	case "SHA256":
		p.ChecksumSHA256 = aws.String(value)
	}
}

func checksumUpload(t *testing.T, st multipartCopyIntegrationStore, algorithm, kind string, p *integrityNativeFixture) (*multipartCopyIntegration, *awss3.CompleteMultipartUploadInput) {
	t.Helper()
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), transferIntegrationConfig(3600), 0)
	client := awss3.New(f.client.Options(), func(o *awss3.Options) {
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenSupported
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenSupported
	})
	f.client = client
	out, err := client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("integrity"), ChecksumAlgorithm: types.ChecksumAlgorithm(algorithm), ChecksumType: types.ChecksumType(kind)})
	if err != nil {
		t.Fatal(err)
	}
	in := &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("integrity"), UploadId: out.UploadId, MultipartUpload: &types.CompletedMultipartUpload{}}
	for n, data := range [][]byte{bytes.Repeat([]byte("a"), int(api.MinMultipartPartBytes)), []byte("last part bytes")} {
		part, err := client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: in.Bucket, Key: in.Key, UploadId: in.UploadId, PartNumber: aws.Int32(int32(n + 1)), Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))), ChecksumAlgorithm: types.ChecksumAlgorithm(algorithm)})
		if err != nil {
			t.Fatal(err)
		}
		x := types.CompletedPart{PartNumber: aws.Int32(int32(n + 1)), ETag: part.ETag}
		h := objectchecksum.NewHash(algorithm)
		_, _ = h.Write(data)
		setSDKPartChecksum(&x, algorithm, base64.StdEncoding.EncodeToString(h.Sum(nil)))
		in.MultipartUpload.Parts = append(in.MultipartUpload.Parts, x)
	}
	return f, in
}

// adr: 1038
func TestMultipartIntegritySDKMem(t *testing.T) {
	for _, p := range []objectchecksum.Policy{{Algorithm: "CRC32", Type: objectchecksum.Composite}, {Algorithm: "CRC32C", Type: objectchecksum.Composite}, {Algorithm: "SHA1", Type: objectchecksum.Composite}, {Algorithm: "SHA256", Type: objectchecksum.Composite}, {Algorithm: "CRC32C", Type: objectchecksum.FullObject}, {Algorithm: "CRC64NVME", Type: objectchecksum.FullObject}} {
		t.Run(p.Algorithm+"/"+p.Type, func(t *testing.T) { multipartIntegritySDK(t, state.NewMemStore(), p) })
	}
}
func TestMultipartIntegritySDKPG(t *testing.T) {
	st, _ := multipartCopyPGStore(t)
	multipartIntegritySDK(t, st, objectchecksum.Policy{Algorithm: "CRC64NVME", Type: objectchecksum.FullObject})
}

func multipartIntegritySDK(t *testing.T, st multipartCopyIntegrationStore, policy objectchecksum.Policy) {
	t.Helper()
	native := &integrityNativeFixture{}
	f, in := checksumUpload(t, st, policy.Algorithm, policy.Type, native)
	complete, err := f.client.CompleteMultipartUpload(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.GetObjectMultipartUpload(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, aws.ToString(in.UploadId))
	if err != nil || !u.Checksum.Complete() || u.State != state.ObjectMultipartCompleted {
		t.Fatalf("durable checksum missing: %+v %v", u, err)
	}
	if aws.ToString(complete.VersionId) == "private-integrity-version" {
		t.Fatal("native version leaked")
	}
	if _, err = f.client.CompleteMultipartUpload(t.Context(), in); err != nil || native.completions != 1 {
		t.Fatal("completion replay changed the native write", err, native.completions)
	}
	read, err := f.client.GetObject(t.Context(), &awss3.GetObjectInput{Bucket: in.Bucket, Key: in.Key, ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(read.Body)
	_ = read.Body.Close()
	if err != nil || !bytes.Equal(b, native.body) {
		t.Fatal("read integrity", err)
	}
	head, err := f.client.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: in.Bucket, Key: in.Key, ChecksumMode: types.ChecksumModeEnabled})
	if err != nil || string(head.ChecksumType) != policy.Type {
		t.Fatal("HEAD checksum missing", head, err)
	}
	attrs, err := f.client.GetObjectAttributes(t.Context(), &awss3.GetObjectAttributesInput{Bucket: in.Bucket, Key: in.Key, VersionId: complete.VersionId, ObjectAttributes: []types.ObjectAttributes{types.ObjectAttributesEtag, types.ObjectAttributesChecksum, types.ObjectAttributesObjectParts, types.ObjectAttributesObjectSize}, MaxParts: aws.Int32(1)})
	if err != nil || attrs.Checksum == nil || attrs.ObjectParts == nil || !aws.ToBool(attrs.ObjectParts.IsTruncated) || len(attrs.ObjectParts.Parts) != 1 || aws.ToString(attrs.VersionId) != aws.ToString(complete.VersionId) {
		t.Fatal("attributes/version/pagination", attrs, err)
	}
	second, err := f.client.GetObjectAttributes(t.Context(), &awss3.GetObjectAttributesInput{Bucket: in.Bucket, Key: in.Key, ObjectAttributes: []types.ObjectAttributes{types.ObjectAttributesObjectParts}, MaxParts: aws.Int32(1), PartNumberMarker: attrs.ObjectParts.NextPartNumberMarker})
	if err != nil || second.ObjectParts == nil || aws.ToBool(second.ObjectParts.IsTruncated) || len(second.ObjectParts.Parts) != 1 || aws.ToInt32(second.ObjectParts.Parts[0].PartNumber) != 2 {
		t.Fatal("second part page", second, err)
	}
}
