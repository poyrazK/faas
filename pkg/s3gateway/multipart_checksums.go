package s3gateway

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type checksumXML struct {
	CRC32     string `xml:"ChecksumCRC32,omitempty"`
	CRC32C    string `xml:"ChecksumCRC32C,omitempty"`
	CRC64NVME string `xml:"ChecksumCRC64NVME,omitempty"`
	SHA1      string `xml:"ChecksumSHA1,omitempty"`
	SHA256    string `xml:"ChecksumSHA256,omitempty"`
}

func checksumXMLFor(d objectchecksum.Digest) checksumXML {
	var out checksumXML
	switch d.Algorithm {
	case "CRC32":
		out.CRC32 = d.Value
	case "CRC32C":
		out.CRC32C = d.Value
	case "CRC64NVME":
		out.CRC64NVME = d.Value
	case "SHA1":
		out.SHA1 = d.Value
	case "SHA256":
		out.SHA256 = d.Value
	}
	return out
}

func (c checksumXML) selected(p objectchecksum.Policy) (string, error) {
	values := map[string]string{"CRC32": c.CRC32, "CRC32C": c.CRC32C, "CRC64NVME": c.CRC64NVME, "SHA1": c.SHA1, "SHA256": c.SHA256}
	for algorithm, value := range values {
		if value != "" && (algorithm != p.Algorithm || !objectchecksum.ValidPart(algorithm, value)) {
			return "", objectchecksum.ErrInvalid
		}
	}
	value := values[p.Algorithm]
	if !p.Empty() && p.Type == objectchecksum.Composite && value == "" {
		return "", objectchecksum.ErrInvalid
	}
	return value, nil
}

func checksumHeader(algorithm string) string { return "X-Amz-Checksum-" + strings.ToLower(algorithm) }

func writeChecksumHeaders(header http.Header, d objectchecksum.Digest, includeType bool) {
	if d.Empty() || d.Value == "" {
		return
	}
	header.Set(checksumHeader(d.Algorithm), d.Value)
	if includeType {
		header.Set("X-Amz-Checksum-Type", d.Type)
	}
}

func requestChecksumPolicy(r *http.Request) (objectchecksum.Policy, error) {
	for _, name := range []string{"X-Amz-Checksum-Algorithm", "X-Amz-Checksum-Type"} {
		if len(r.Header.Values(name)) > 1 {
			return objectchecksum.Policy{}, objectchecksum.ErrInvalid
		}
	}
	return objectchecksum.NewPolicy(r.Header.Get("X-Amz-Checksum-Algorithm"), r.Header.Get("X-Amz-Checksum-Type"))
}

func (h *Handler) multipartChecksumPolicy(w http.ResponseWriter, r *http.Request, req requestContext) (objectchecksum.Policy, bool) {
	for _, name := range awsChecksumHeaderNames {
		if len(r.Header.Values(name)) != 0 {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidRequest")
			return objectchecksum.Policy{}, false
		}
	}
	p, err := requestChecksumPolicy(r)
	if err != nil {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidRequest")
		return p, false
	}
	if !p.Empty() {
		provider, ok := req.provider.(objectstorage.MultipartChecksumProvider)
		if !ok || !provider.SupportsMultipartChecksum(p) {
			h.unsupported(w, r, req.requestID)
			return p, false
		}
	}
	return p, true
}

func configurePartChecksum(r *http.Request, req requestContext, policy objectchecksum.Policy, body *requestIntegrityReader) error {
	if r.Header.Get("X-Amz-Checksum-Type") != "" {
		return invalidAWSChunked("Checksum type is selected when the upload is created.")
	}
	if policy.Empty() {
		return nil
	}
	for _, name := range awsChecksumHeaderNames {
		if r.Header.Get(name) != "" && !strings.EqualFold(name, checksumHeader(policy.Algorithm)) {
			return invalidAWSChunked("The part checksum must match the upload algorithm.")
		}
	}
	if req.streaming != nil && req.streaming.trailerName != "" && !strings.EqualFold(req.streaming.trailerName, checksumHeader(policy.Algorithm)) {
		return invalidAWSChunked("The checksum trailer must match the upload algorithm.")
	}
	body.multipartChecksum = objectchecksum.NewHash(policy.Algorithm)
	return nil
}

func completionChecksumRequest(r *http.Request, s objectchecksum.Snapshot, size int64) error {
	if len(r.Header.Values("X-Amz-Checksum-Type")) > 1 || r.Header.Get("X-Amz-Checksum-Type") != "" && r.Header.Get("X-Amz-Checksum-Type") != s.Type {
		return invalidAWSChunked("Checksum type does not match the upload.")
	}
	for _, name := range awsChecksumHeaderNames {
		if len(r.Header.Values(name)) > 1 {
			return invalidAWSChunked("Duplicate checksum header.")
		}
		if v := r.Header.Get(name); v != "" && (!strings.EqualFold(name, checksumHeader(s.Algorithm)) || v != s.Value) {
			return badAWSChunkDigest()
		}
	}
	if values := r.Header.Values("X-Amz-Mp-Object-Size"); len(values) > 0 {
		if len(values) != 1 || values[0] != strconv.FormatInt(size, 10) {
			return invalidAWSChunked("The declared multipart object size does not match the selected parts.")
		}
	}
	return nil
}

func (h *Handler) bindCompletionChecksum(w http.ResponseWriter, r *http.Request, req requestContext, u *state.ObjectMultipartUpload, parts []api.ObjectMultipartCompletedPart, values []string, native map[int32]objectstorage.MultipartPart, total int64) bool {
	if !u.Checksum.Empty() {
		selected := make([]objectchecksum.Part, len(parts))
		for i, part := range parts {
			p := native[part.PartNumber]
			if p.Checksum.Policy != u.Checksum.Policy || !objectchecksum.ValidPart(u.Checksum.Algorithm, p.Checksum.Value) {
				h.providerError(w, r, req, objectstorage.ErrUnavailable, u.Key)
				return false
			}
			if values[i] != "" && values[i] != p.Checksum.Value {
				h.writeAWSChunkedError(w, r, req.requestID, badAWSChunkDigest())
				return false
			}
			selected[i] = objectchecksum.Part{Number: part.PartNumber, Size: p.SizeBytes, Value: p.Checksum.Value}
		}
		s, err := objectchecksum.Build(u.Checksum.Policy, selected)
		if err != nil {
			h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidPartOrder")
			return false
		}
		u.Checksum = s
	}
	if err := completionChecksumRequest(r, u.Checksum, total); err != nil {
		h.writeAWSChunkedError(w, r, req.requestID, err)
		return false
	}
	return true
}

func partDigest(u state.ObjectMultipartUpload, body *requestIntegrityReader) objectchecksum.Digest {
	if body.multipartChecksum == nil {
		return objectchecksum.Digest{}
	}
	return objectchecksum.Digest{Policy: u.Checksum.Policy, Value: base64.StdEncoding.EncodeToString(body.multipartChecksum.Sum(nil))}
}
