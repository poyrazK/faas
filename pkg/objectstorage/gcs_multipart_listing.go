package objectstorage

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Discovery must prove a complete, unambiguous exact-key result before a new
// initiation. An incomplete page cannot establish that an earlier upload is absent.
func (p *GCS) discoverGCSMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
	found, keyMarker, uploadMarker := "", "", ""
	seen := map[string]bool{}
	for range api.ObjectMultipartInitiationMaxPages {
		q := url.Values{"uploads": {""}, "prefix": {r.Key}, "max-uploads": {strconv.Itoa(api.MaxObjectS3ListItems)}, "encoding-type": {"url"}}
		if keyMarker != "" {
			q.Set("key-marker", keyMarker)
		}
		if uploadMarker != "" {
			q.Set("upload-id-marker", uploadMarker)
		}
		if r.BeforeRequest != nil {
			if err := r.BeforeRequest(ctx); err != nil {
				return "", err
			}
		}
		var out gcsListMultipartUploadsResult
		if err := p.xmlRequest(ctx, http.MethodGet, bucket, "", q, nil, nil, &out); err != nil {
			return "", normalizeGCS(err)
		}
		encoding, err := gcsVersionListEncoding(gcsVersionListXML{Encoding: out.Encoding, NativeEncoding: out.NativeEncoding})
		if err != nil || out.IsTruncated == nil || len(out.Uploads) > api.MaxObjectS3ListItems || len(out.CommonPrefixes) != 0 {
			return "", ErrUnavailable
		}
		last := keyMarker
		for _, upload := range out.Uploads {
			key, err := gcsVersionListKey(upload.Key, encoding)
			identity := key + "\x00" + upload.UploadID
			if err != nil || !ValidKey(key) || !strings.HasPrefix(key, r.Key) || key < last || !validMultipartUploadID(upload.UploadID) || seen[identity] {
				return "", ErrUnavailable
			}
			last, seen[identity] = key, true
			if key == r.Key {
				if found != "" {
					return "", ErrConflict
				}
				found = upload.UploadID
			}
		}
		if !*out.IsTruncated {
			if out.NextKeyMarker != "" || out.NextUploadIDMarker != "" {
				return "", ErrUnavailable
			}
			return found, nil
		}
		key, err := gcsVersionListKey(out.NextKeyMarker, encoding)
		if err != nil || len(out.Uploads) == 0 || key != last || !validMultipartUploadID(out.NextUploadIDMarker) || out.NextUploadIDMarker != out.Uploads[len(out.Uploads)-1].UploadID || key == keyMarker && out.NextUploadIDMarker == uploadMarker {
			return "", ErrUnavailable
		}
		keyMarker, uploadMarker = key, out.NextUploadIDMarker
	}
	return "", ErrUnavailable
}

func gcsMultipartPartsPage(out gcsListPartsResult, bucket string, r MultipartListPartsRequest) (MultipartPartsPage, error) {
	if out.IsTruncated == nil || len(out.Parts) > int(r.Limit) || out.Bucket != "" && out.Bucket != bucket || out.Key != "" && out.Key != r.Key || out.UploadID != "" && out.UploadID != r.ProviderUploadID {
		return MultipartPartsPage{}, ErrUnavailable
	}
	page := MultipartPartsPage{Items: make([]MultipartPart, 0, len(out.Parts))}
	last := r.PartNumberMarker
	for _, part := range out.Parts {
		if part.PartNumber <= last || part.PartNumber > api.MaxMultipartParts || !validUploadETag(part.ETag) || part.Size < 1 || part.Size > api.MaxObjectSinglePutBytes || part.LastModified.IsZero() {
			return MultipartPartsPage{}, ErrUnavailable
		}
		last = part.PartNumber
		page.Items = append(page.Items, MultipartPart{PartNumber: part.PartNumber, ETag: part.ETag, SizeBytes: part.Size, LastModified: part.LastModified})
	}
	if *out.IsTruncated {
		if len(out.Parts) == 0 || out.NextPartNumberMarker != last {
			return MultipartPartsPage{}, ErrUnavailable
		}
		page.NextPartNumberMarker = last
	} else if out.NextPartNumberMarker != 0 {
		return MultipartPartsPage{}, ErrUnavailable
	}
	return page, nil
}
