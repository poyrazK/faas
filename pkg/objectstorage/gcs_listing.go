package objectstorage

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/api/option"
	gcsapi "google.golang.org/api/storage/v1"
)

func newGCSObjectListingClient(ctx context.Context, client *http.Client, endpoint string) (*gcsapi.ObjectsService, error) {
	opts := []option.ClientOption{option.WithHTTPClient(client)}
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}
	service, err := gcsapi.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return service.Objects, nil
}

// The high-level SDK's pager refills short pages and retries transient errors.
// Use one JSON API exchange so the caller reserves every native page/attempt.
func (s *googleGCSStore) listObjectPage(ctx context.Context, bucket, prefix, delimiter, start, cursor string, limit int32) (*gcsapi.Objects, error) {
	if s.objects == nil {
		return nil, ErrConfiguration
	}
	if bucket == "" || limit < 1 || limit > api.MaxObjectS3ListItems {
		return nil, ErrInvalid
	}
	page, err := s.objects.List(bucket).Projection("noAcl").Prefix(prefix).Delimiter(delimiter).
		StartOffset(start).PageToken(cursor).MaxResults(int64(limit)).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if page == nil || len(page.Items)+len(page.Prefixes) > int(limit) || len(page.NextPageToken) > api.ObjectVersionInventoryCursorMaxBytes || page.NextPageToken != "" && page.NextPageToken == cursor {
		return nil, ErrUnavailable
	}
	return page, nil
}

func gcsListedObject(item *gcsapi.Object) (gcsObjectState, error) {
	if item == nil || !ValidKey(item.Name) || item.Size > uint64(api.MaxObjectUploadBytes) {
		return gcsObjectState{}, ErrUnavailable
	}
	modified := item.Updated
	out := gcsObjectState{Key: item.Name, ETag: item.Etag, Size: int64(item.Size), Version: item.Generation,
		MetaVersion: item.Metageneration, Metadata: item.Metadata, KMSKeyName: item.KmsKeyName}
	var err error
	if modified != "" {
		out.LastModified, err = time.Parse(time.RFC3339Nano, modified)
		if err != nil {
			return gcsObjectState{}, ErrUnavailable
		}
	}
	if item.TimeDeleted != "" {
		out.ValidUntil, err = time.Parse(time.RFC3339Nano, item.TimeDeleted)
		if err != nil {
			return gcsObjectState{}, ErrUnavailable
		}
	}
	return out, nil
}
