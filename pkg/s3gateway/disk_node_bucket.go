package s3gateway

import (
	"encoding/xml"
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

func (h *DiskHandler) bucket(w http.ResponseWriter, r *http.Request, bucket, id string) {
	q := operationQuery(r.URL.Query())
	if r.Method == http.MethodGet && q.Has("uploads") {
		h.listMultipart(w, r, bucket, id)
		return
	}
	if r.Method == http.MethodGet && q.Get("list-type") == "2" {
		h.list(w, r, bucket, id)
		return
	}
	if r.Method == http.MethodGet && q.Has("versioning") && queryKeysOnly(q, "versioning") {
		if err := h.config.Store.HeadBucket(bucket); err != nil {
			h.failure(w, r, id, err)
			return
		}
		writeS3XML(w, 200, id, struct {
			XMLName xml.Name `xml:"VersioningConfiguration"`
			XMLNS   string   `xml:"xmlns,attr"`
		}{XMLNS: s3XMLNamespace})
		return
	}
	if len(q) != 0 || r.ContentLength != 0 || r.Method != http.MethodPut && r.Method != http.MethodHead && r.Method != http.MethodDelete {
		h.unsupported(w, r, id)
		return
	}
	var err error
	status := http.StatusOK
	switch r.Method {
	case http.MethodPut:
		err = h.config.Store.CreateBucket(bucket)
	case http.MethodHead:
		err = h.config.Store.HeadBucket(bucket)
	case http.MethodDelete:
		err, status = h.config.Store.DeleteBucket(bucket), http.StatusNoContent
	}
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	w.WriteHeader(status)
}

func (h *DiskHandler) list(w http.ResponseWriter, r *http.Request, bucket, id string) {
	q := operationQuery(r.URL.Query())
	if !queryKeysOnly(q, "list-type", "prefix", "delimiter", "continuation-token", "start-after", "max-keys", "encoding-type") {
		h.unsupported(w, r, id)
		return
	}
	limit, err := listLimit(q, "max-keys")
	if err != nil || q.Get("encoding-type") != "" && q.Get("encoding-type") != "url" {
		h.failure(w, r, id, objectnode.ErrInvalid)
		return
	}
	page, err := h.config.Store.List(r.Context(), bucket, objectnode.ListRequest{Prefix: q.Get("prefix"), Delimiter: q.Get("delimiter"), Cursor: q.Get("continuation-token"), StartAfter: q.Get("start-after"), Limit: limit})
	if err != nil {
		h.failure(w, r, id, err)
		return
	}
	objects := objectstorage.ObjectPage{NextCursor: page.NextCursor}
	for _, entry := range page.Entries {
		if entry.CommonPrefix {
			objects.CommonPrefixes = append(objects.CommonPrefixes, entry.Key)
		} else {
			objects.Items = append(objects.Items, objectstorage.Object{Key: entry.Key, ETag: entry.ETag, Size: entry.Size, LastModified: entry.Modified})
		}
	}
	writeS3XML(w, 200, id, encodedObjectsResult(bucket, q, limit, objects))
}
