package s3gateway

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func listLimit(q url.Values, name string) (int32, error) {
	if q.Get(name) == "" {
		return api.MaxObjectS3ListItems, nil
	}
	n, e := strconv.ParseInt(q.Get(name), 10, 32)
	if e != nil || n < 0 || n > api.MaxObjectS3ListItems {
		return 0, objectstorage.ErrInvalid
	}
	return int32(n), nil
}
func validListText(s string) bool {
	return len(s) <= api.MaxObjectS3ListTextBytes && utf8.ValidString(s)
}
func encodeListText(s string, enabled bool) string {
	if enabled {
		return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
	}
	return s
}

func (h *Handler) listObjectsV2(w http.ResponseWriter, r *http.Request, req requestContext, q url.Values) {
	if !queryKeysOnly(q, "list-type", "prefix", "delimiter", "continuation-token", "start-after", "max-keys", "encoding-type") {
		h.unsupported(w, r, req.requestID)
		return
	}
	if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
		return
	}
	limit, err := listLimit(q, "max-keys")
	prefix, delimiter, cursor, start := q.Get("prefix"), q.Get("delimiter"), q.Get("continuation-token"), q.Get("start-after")
	encoding := q.Get("encoding-type")
	if err != nil || !validListText(prefix) || !validListText(start) || len(cursor) > api.MaxObjectS3ListCursorBytes || !validDelimiter(delimiter) || encoding != "" && encoding != "url" {
		writeS3Error(w, http.StatusBadRequest, "InvalidArgument", "A query parameter is invalid.", r.URL.Path, req.requestID)
		return
	}
	if !h.admit(w, r, req, "__list__", 0, false) {
		return
	}
	page := objectstorage.ObjectPage{}
	if limit > 0 {
		if !h.recordProviderRequest(w, r, req) {
			return
		}
		page, err = listProviderObjects(r.Context(), req.provider, req.bucket.PhysicalName, objectstorage.ObjectListRequest{Prefix: prefix, Delimiter: delimiter, Cursor: cursor, StartAfter: start, Limit: limit})
		if err != nil {
			h.providerError(w, r, req, err, "")
			return
		}
	}
	writeS3XML(w, http.StatusOK, req.requestID, encodedObjectsResult(req.bucket.Name, q, limit, page))
}
func (h *Handler) listMultipartUploads(w http.ResponseWriter, r *http.Request, req requestContext) {
	if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
		return
	}
	q := operationQuery(r.URL.Query())
	if !queryKeysOnly(q, "uploads", "prefix", "max-uploads", "key-marker", "upload-id-marker", "encoding-type") {
		h.unsupported(w, r, req.requestID)
		return
	}
	limit, err := listLimit(q, "max-uploads")
	prefix, keyMarker, uploadMarker := q.Get("prefix"), q.Get("key-marker"), q.Get("upload-id-marker")
	encoding := q.Get("encoding-type")
	if err != nil || !validListText(prefix) || !validListText(keyMarker) || len(uploadMarker) > api.MaxObjectS3UploadMarkerBytes || encoding != "" && encoding != "url" {
		h.writeMultipartError(w, r, req, objectstorage.ErrInvalid, "InvalidArgument")
		return
	}
	lister, ok := h.multipartStore.(state.ObjectS3MultipartLister)
	if !ok {
		h.unsupported(w, r, req.requestID)
		return
	}
	result := listMultipartUploadsResult{XMLNS: s3XMLNamespace, Bucket: req.bucket.Name, Prefix: prefix, KeyMarker: keyMarker, UploadMarker: uploadMarker, EncodingType: encoding, MaxUploads: limit}
	if limit > 0 {
		rows, e := lister.ListObjectS3MultipartUploads(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID, prefix, keyMarker, uploadMarker, limit+1)
		if e != nil {
			h.writeMultipartError(w, r, req, e, "ServiceUnavailable")
			return
		}
		if len(rows) > int(limit) {
			result.IsTruncated = true
			rows = rows[:limit]
			last := rows[len(rows)-1]
			result.NextKeyMarker = last.Key
			result.NextUploadMarker = last.ID
		}
		for _, u := range rows {
			result.Uploads = append(result.Uploads, listedMultipartUpload{Key: encodeListText(u.Key, encoding == "url"), UploadID: u.ID, Initiated: u.CreatedAt.UTC().Format(time.RFC3339Nano)})
		}
	}
	result.Prefix = encodeListText(result.Prefix, encoding == "url")
	result.KeyMarker = encodeListText(result.KeyMarker, encoding == "url")
	result.NextKeyMarker = encodeListText(result.NextKeyMarker, encoding == "url")
	writeS3XML(w, http.StatusOK, req.requestID, result)
}

func listProviderObjects(ctx context.Context, p objectstorage.Provider, bucket string, req objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	if lister, ok := p.(objectstorage.ObjectV2Lister); ok {
		return lister.ListObjectsV2(ctx, bucket, req)
	}
	if req.StartAfter != "" {
		return objectstorage.ObjectPage{}, objectstorage.ErrUnsupported
	}
	if req.Delimiter == "" {
		return p.ListObjects(ctx, bucket, req.Prefix, req.Cursor, req.Limit)
	}
	if lister, ok := p.(objectstorage.DelimitedObjectLister); ok {
		return lister.ListObjectsDelimited(ctx, bucket, req.Prefix, req.Delimiter, req.Cursor, req.Limit)
	}
	return objectstorage.ObjectPage{}, objectstorage.ErrUnsupported
}
func encodedObjectsResult(bucket string, q url.Values, limit int32, page objectstorage.ObjectPage) listBucketResult {
	prefix, delimiter, cursor, start, encoding := q.Get("prefix"), q.Get("delimiter"), q.Get("continuation-token"), q.Get("start-after"), q.Get("encoding-type")
	result := listObjectsResult(bucket, prefix, limit, page)
	result.Delimiter = delimiter
	result.ContinuationToken = cursor
	result.StartAfter = start
	result.EncodingType = encoding
	if encoding == "url" {
		result.Prefix = encodeListText(prefix, true)
		result.Delimiter = encodeListText(delimiter, true)
		result.StartAfter = encodeListText(start, true)
		for i := range result.Contents {
			result.Contents[i].Key = encodeListText(result.Contents[i].Key, true)
		}
		for i := range result.CommonPrefixes {
			result.CommonPrefixes[i].Prefix = encodeListText(result.CommonPrefixes[i].Prefix, true)
		}
	}
	return result
}
