package s3gateway

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func (h *DiskHandler) listParts(w http.ResponseWriter, r *http.Request, bucket, key, uploadID, id string) {
	q := operationQuery(r.URL.Query())
	limit, err := listLimit(q, "max-parts")
	if err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	marker := int64(0)
	if q.Get("part-number-marker") != "" {
		marker, err = strconv.ParseInt(q.Get("part-number-marker"), 10, 32)
	}
	if err != nil {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	u, err := h.config.Store.MultipartInfo(bucket, key, uploadID)
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	parts, truncated, err := h.config.Store.ListParts(bucket, key, uploadID, int32(marker), int(limit))
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	result := listMultipartPartsResult{XMLNS: s3XMLNamespace, Bucket: bucket, Key: key, UploadID: uploadID, PartNumberMarker: int32(marker), MaxParts: limit, IsTruncated: truncated, ChecksumAlgorithm: u.Checksum.Algorithm, ChecksumType: u.Checksum.Type}
	for _, part := range parts {
		o := part.Object
		result.Parts = append(result.Parts, listedMultipartPart{PartNumber: part.Number, ETag: o.ETag, Size: o.Size, LastModified: o.Modified.UTC().Format(time.RFC3339Nano), checksumXML: checksumXMLFor(objectchecksum.Digest{Policy: objectchecksum.Policy{Algorithm: o.ChecksumAlgorithm}, Value: o.Checksum})})
	}
	if truncated && len(parts) != 0 {
		result.NextPartNumberMarker = parts[len(parts)-1].Number
	}
	writeS3XML(w, http.StatusOK, id, result)
}

func (h *DiskHandler) listMultipart(w http.ResponseWriter, r *http.Request, bucket, id string) {
	q := operationQuery(r.URL.Query())
	if !diskMultipartQuery(q, "uploads", "prefix", "key-marker", "upload-id-marker", "max-uploads", "encoding-type") {
		h.unsupported(w, r, id)
		return
	}
	limit, err := listLimit(q, "max-uploads")
	encoding := q.Get("encoding-type")
	if err != nil || encoding != "" && encoding != "url" {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	items, truncated, err := h.config.Store.ListMultipart(r.Context(), bucket, objectnode.MultipartListRequest{Prefix: q.Get("prefix"), KeyMarker: q.Get("key-marker"), UploadMarker: q.Get("upload-id-marker"), Limit: int(limit)})
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	encode := encoding == "url"
	result := listMultipartUploadsResult{XMLNS: s3XMLNamespace, Bucket: bucket, EncodingType: encoding, Prefix: encodeListText(q.Get("prefix"), encode), KeyMarker: encodeListText(q.Get("key-marker"), encode), UploadMarker: q.Get("upload-id-marker"), MaxUploads: limit, IsTruncated: truncated}
	for _, u := range items {
		result.Uploads = append(result.Uploads, listedMultipartUpload{Key: encodeListText(u.Key, encode), UploadID: u.ID, Initiated: u.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	if truncated && len(items) != 0 {
		last := items[len(items)-1]
		result.NextKeyMarker, result.NextUploadMarker = encodeListText(last.Key, encode), last.ID
	}
	writeS3XML(w, http.StatusOK, id, result)
}
