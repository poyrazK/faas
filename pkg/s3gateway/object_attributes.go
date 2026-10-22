package s3gateway

import (
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type attributeChecksumXML struct {
	checksumXML
	Type string `xml:"ChecksumType,omitempty"`
}
type attributePartXML struct {
	checksumXML
	Number int32 `xml:"PartNumber"`
	Size   int64 `xml:"Size"`
}
type attributePartsXML struct {
	Count     int32              `xml:"PartsCount"`
	Marker    int32              `xml:"PartNumberMarker"`
	Next      int32              `xml:"NextPartNumberMarker,omitempty"`
	Max       int32              `xml:"MaxParts"`
	Truncated bool               `xml:"IsTruncated"`
	Parts     []attributePartXML `xml:"Part,omitempty"`
}
type objectAttributesXML struct {
	XMLName      xml.Name              `xml:"GetObjectAttributesResponse"`
	XMLNS        string                `xml:"xmlns,attr"`
	ETag         string                `xml:"ETag,omitempty"`
	Size         *int64                `xml:"ObjectSize,omitempty"`
	StorageClass string                `xml:"StorageClass,omitempty"`
	Checksum     *attributeChecksumXML `xml:"Checksum,omitempty"`
	Parts        *attributePartsXML    `xml:"ObjectParts,omitempty"`
}

func objectChecksumsXML(c objectstorage.ObjectChecksums) checksumXML {
	return checksumXML{CRC32: c.CRC32, CRC32C: c.CRC32C, CRC64NVME: c.CRC64NVME, SHA1: c.SHA1, SHA256: c.SHA256}
}

func (h *Handler) objectAttributes(w http.ResponseWriter, r *http.Request, req requestContext, key string) {
	if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
		return
	}
	provider, ok := req.provider.(objectstorage.ObjectAttributesProvider)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return
	}
	input, ok := h.parseObjectAttributesRequest(w, r, req, key)
	if !ok {
		return
	}
	native, ok := h.resolveReadVersion(w, r, req, key)
	if !ok {
		return
	}
	input.ProviderVersionID = native
	if !h.admit(w, r, req, key, 0, false) || !h.recordProviderRequest(w, r, req) {
		return
	}
	out, err := provider.GetObjectAttributes(r.Context(), req.bucket.PhysicalName, input)
	if err != nil {
		h.objectAttributesError(w, r, req, key, native, err)
		return
	}
	if !h.publicVersionHeader(w, r, req, key, out.ProviderVersionID, false) {
		return
	}
	if !out.LastModified.IsZero() {
		w.Header().Set("Last-Modified", out.LastModified.UTC().Format(http.TimeFormat))
	}
	writeObjectAttributes(w, req, out)
}

func (h *Handler) parseObjectAttributesRequest(w http.ResponseWriter, r *http.Request, req requestContext, key string) (objectstorage.ObjectAttributesRequest, bool) {
	input, err := objectAttributesRequest(r, key)
	if err == nil {
		return input, true
	}
	if errors.Is(err, objectstorage.ErrUnsupported) {
		h.unsupported(w, r, req.requestID)
	} else {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
	}
	return input, false
}

func (h *Handler) objectAttributesError(w http.ResponseWriter, r *http.Request, req requestContext, key, native string, err error) {
	var marker *objectstorage.ObjectAttributesDeleteMarker
	if errors.As(err, &marker) {
		if !h.publicVersionHeader(w, r, req, key, marker.ProviderVersionID, true) {
			return
		}
		w.Header().Set("X-Amz-Delete-Marker", "true")
		if native != "" {
			if !marker.LastModified.IsZero() {
				w.Header().Set("Last-Modified", marker.LastModified.UTC().Format(http.TimeFormat))
			}
			writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified version is a delete marker.", r.URL.Path, req.requestID)
			return
		}
		h.providerError(w, r, req, objectstorage.ErrNotFound, key)
		return
	}
	if native != "" && errors.Is(err, objectstorage.ErrNotFound) {
		writeS3Error(w, http.StatusNotFound, "NoSuchVersion", "The specified version does not exist.", r.URL.Path, req.requestID)
		return
	}
	h.providerError(w, r, req, err, key)
}

func objectAttributesRequest(r *http.Request, key string) (objectstorage.ObjectAttributesRequest, error) {
	attributeHeader := strings.Join(r.Header.Values("X-Amz-Object-Attributes"), ",")
	if attributeHeader == "" || len(attributeHeader) > api.MaxObjectAttributesHeaderBytes {
		return objectstorage.ObjectAttributesRequest{}, objectstorage.ErrInvalid
	}
	attrs := strings.Split(attributeHeader, ",")
	for i := range attrs {
		attrs[i] = strings.TrimSpace(attrs[i])
	}
	input := objectstorage.ObjectAttributesRequest{Key: key, Attributes: attrs, MaxParts: api.MaxObjectS3ListItems}
	for _, item := range []struct {
		name   string
		target *int32
		max    int32
	}{{"X-Amz-Max-Parts", &input.MaxParts, api.MaxObjectS3ListItems}, {"X-Amz-Part-Number-Marker", &input.PartNumberMarker, api.MaxMultipartParts}} {
		if values := r.Header.Values(item.name); len(values) > 0 {
			n, e := strconv.ParseInt(values[0], 10, 32)
			if len(values) != 1 || e != nil || n < 0 || n > int64(item.max) || item.name == "X-Amz-Max-Parts" && n == 0 {
				return objectstorage.ObjectAttributesRequest{}, objectstorage.ErrInvalid
			}
			*item.target = int32(n)
		}
	}
	for _, name := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if r.Header.Get(name) != "" {
			return objectstorage.ObjectAttributesRequest{}, objectstorage.ErrUnsupported
		}
	}
	if !objectstorage.ValidObjectAttributesRequest(input) {
		return objectstorage.ObjectAttributesRequest{}, objectstorage.ErrInvalid
	}
	return input, nil
}

func writeObjectAttributes(w http.ResponseWriter, req requestContext, out objectstorage.ObjectAttributes) {
	result := objectAttributesXML{XMLNS: s3XMLNamespace, ETag: out.ETag, Size: out.Size, StorageClass: out.StorageClass}
	if out.Checksums != nil {
		result.Checksum = &attributeChecksumXML{checksumXML: objectChecksumsXML(*out.Checksums), Type: out.Checksums.Type}
	}
	if out.Parts != nil {
		p := out.Parts
		result.Parts = &attributePartsXML{Count: p.Count, Marker: p.Marker, Next: p.NextMarker, Max: p.MaxParts, Truncated: p.Truncated}
		for _, part := range p.Parts {
			result.Parts.Parts = append(result.Parts.Parts, attributePartXML{checksumXML: objectChecksumsXML(part.Checksums), Number: part.Number, Size: part.Size})
		}
	}
	writeS3XML(w, http.StatusOK, req.requestID, result)
}

func validReadChecksumHeaders(header http.Header) bool {
	if len(header.Values("X-Amz-Checksum-Type")) > 1 {
		return false
	}
	c := objectstorage.ObjectChecksums{Type: header.Get("X-Amz-Checksum-Type")}
	for _, item := range []struct {
		name   string
		target *string
	}{{"CRC32", &c.CRC32}, {"CRC32C", &c.CRC32C}, {"CRC64NVME", &c.CRC64NVME}, {"SHA1", &c.SHA1}, {"SHA256", &c.SHA256}} {
		name := checksumHeader(item.name)
		if len(header.Values(name)) > 1 {
			return false
		}
		*item.target = header.Get(name)
	}
	return objectstorage.ValidateObjectChecksums(c, false)
}
