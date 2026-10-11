package s3gateway

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type listVersionsResult struct {
	XMLName           xml.Name        `xml:"ListVersionsResult"`
	XMLNS             string          `xml:"xmlns,attr"`
	Name              string          `xml:"Name"`
	Prefix            string          `xml:"Prefix"`
	Delimiter         string          `xml:"Delimiter,omitempty"`
	KeyMarker         string          `xml:"KeyMarker"`
	VersionMarker     string          `xml:"VersionIdMarker"`
	NextKeyMarker     string          `xml:"NextKeyMarker,omitempty"`
	NextVersionMarker string          `xml:"NextVersionIdMarker,omitempty"`
	EncodingType      string          `xml:"EncodingType,omitempty"`
	MaxKeys           int32           `xml:"MaxKeys"`
	IsTruncated       bool            `xml:"IsTruncated"`
	Versions          []listedVersion `xml:"Version"`
	DeleteMarkers     []listedVersion `xml:"DeleteMarker"`
	CommonPrefixes    []commonPrefix  `xml:"CommonPrefixes,omitempty"`
}

type listedVersion struct {
	Key          string `xml:"Key"`
	VersionID    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag,omitempty"`
	Size         *int64 `xml:"Size,omitempty"`
	StorageClass string `xml:"StorageClass,omitempty"`
}

func (h *Handler) listObjectVersions(w http.ResponseWriter, r *http.Request, req requestContext, q url.Values) {
	if !queryKeysOnly(q, "versions", "prefix", "delimiter", "key-marker", "version-id-marker", "encoding-type", "max-keys") {
		h.unsupported(w, r, req.requestID)
		return
	}
	if !h.require(w, req, state.ObjectBucketPermissionRead, r.URL.Path) {
		return
	}
	st, owned := h.store.(state.ObjectVersionReferenceStore)
	lister, capable := req.provider.(objectstorage.ObjectVersionLister)
	if !owned || !capable {
		h.unsupported(w, r, req.requestID)
		return
	}
	input, err := versionListInput(r.Context(), req, st, q)
	if errors.Is(err, objectstorage.ErrInvalid) || errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, http.StatusBadRequest, "InvalidArgument", "A query parameter is invalid.", r.URL.Path, req.requestID)
		return
	}
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, "")
		return
	}
	if !h.admit(w, r, req, "__versions__", 0, false) {
		return
	}
	page := objectstorage.ObjectVersionListPage{}
	if input.Limit > 0 {
		if !h.recordProviderRequest(w, r, req) {
			return
		}
		page, err = lister.ListObjectVersionPage(r.Context(), req.bucket.PhysicalName, input)
		if err != nil {
			h.providerError(w, r, req, err, "")
			return
		}
	}
	result, err := h.customerVersionPage(r.Context(), req, st, q, input, page)
	if err != nil {
		h.providerError(w, r, req, objectstorage.ErrUnavailable, "")
		return
	}
	writeS3XML(w, http.StatusOK, req.requestID, result)
}

func versionListInput(ctx context.Context, req requestContext, st state.ObjectVersionReferenceStore, q url.Values) (objectstorage.ObjectVersionListRequest, error) {
	limit, err := listLimit(q, "max-keys")
	input := objectstorage.ObjectVersionListRequest{Prefix: q.Get("prefix"), Delimiter: q.Get("delimiter"), KeyMarker: q.Get("key-marker"), Limit: limit}
	if err != nil || !validListText(input.Prefix) || !validListText(input.KeyMarker) || input.Prefix != "" && !objectstorage.ValidKey(input.Prefix) || input.KeyMarker != "" && !objectstorage.ValidKey(input.KeyMarker) || !validDelimiter(input.Delimiter) || q.Get("encoding-type") != "" && q.Get("encoding-type") != "url" || q.Get("version-id-marker") != "" && (input.KeyMarker == "" || !state.ValidObjectVersionID(q.Get("version-id-marker"))) {
		return input, objectstorage.ErrInvalid
	}
	if id := q.Get("version-id-marker"); id != "" {
		input.ProviderVersionMarker, err = st.ResolveObjectVersion(ctx, req.bucket.AccountID, req.bucket.ID, input.KeyMarker, id)
	}
	return input, err
}

func (h *Handler) customerVersionPage(parent context.Context, req requestContext, st state.ObjectVersionReferenceStore, q url.Values, input objectstorage.ObjectVersionListRequest, page objectstorage.ObjectVersionListPage) (listVersionsResult, error) {
	public, err := objectstorage.PublicObjectVersionPage(parent, st, req.bucket, input, page)
	if err != nil {
		return listVersionsResult{}, err
	}
	result := listVersionsResult{XMLNS: s3XMLNamespace, Name: req.bucket.Name, Prefix: q.Get("prefix"), Delimiter: q.Get("delimiter"), KeyMarker: q.Get("key-marker"), VersionMarker: q.Get("version-id-marker"), EncodingType: q.Get("encoding-type"), MaxKeys: input.Limit, IsTruncated: public.NextKeyMarker != "", NextKeyMarker: public.NextKeyMarker, NextVersionMarker: public.NextVersionIDMarker}
	for _, v := range public.Items {
		item := listedVersion{Key: v.Key, VersionID: v.VersionID, IsLatest: v.IsLatest, LastModified: v.LastModified.UTC().Format(time.RFC3339Nano), ETag: v.ETag}
		if v.DeleteMarker {
			result.DeleteMarkers = append(result.DeleteMarkers, item)
		} else {
			size := v.SizeBytes
			item.Size = &size
			item.StorageClass = v.StorageClass
			result.Versions = append(result.Versions, item)
		}
	}

	encoded := q.Get("encoding-type") == "url"
	result.Prefix, result.Delimiter, result.KeyMarker, result.NextKeyMarker = encodeListText(result.Prefix, encoded), encodeListText(result.Delimiter, encoded), encodeListText(result.KeyMarker, encoded), encodeListText(result.NextKeyMarker, encoded)
	for i := range result.Versions {
		result.Versions[i].Key = encodeListText(result.Versions[i].Key, encoded)
	}
	for i := range result.DeleteMarkers {
		result.DeleteMarkers[i].Key = encodeListText(result.DeleteMarkers[i].Key, encoded)
	}
	for _, p := range public.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, commonPrefix{Prefix: encodeListText(p, encoded)})
	}
	return result, nil
}
