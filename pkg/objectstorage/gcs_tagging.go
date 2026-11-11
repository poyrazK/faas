package objectstorage

import (
	"context"
	"errors"
	"maps"
	"net/http"
)

// Preparation reads the current identity and reserves every native attempt
// before the caller records a writer. The patch targets that identity only.
func (p *GCS) prepareObjectTagging(ctx context.Context, bucket, method, key string, tags map[string]string, beforeRequest func(context.Context) error) (func(context.Context) (ObjectTaggingResult, error), error) {
	if !ValidKey(key) || ValidateObjectMetadata(ObjectMetadata{Tags: tags}) != nil {
		return nil, ErrInvalid
	}
	if method == http.MethodDelete {
		tags = nil
	}
	encoded, err := EncodeObjectTags(tags)
	if err != nil {
		return nil, err
	}
	reserve := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if beforeRequest != nil {
			return beforeRequest(ctx)
		}
		return nil
	}
	if err := reserve(); err != nil {
		return nil, err
	}
	if method == http.MethodGet {
		return func(ctx context.Context) (ObjectTaggingResult, error) {
			tags, err := p.GetObjectTags(ctx, bucket, key)
			return ObjectTaggingResult{Tags: tags}, err
		}, nil
	}
	object, err := p.store.ObjectState(ctx, bucket, key)
	if err != nil {
		return nil, normalizeGCS(err)
	}
	if object.Key != key || object.Version <= 0 || object.MetaVersion <= 0 {
		return nil, ErrUnavailable
	}
	if err := reserve(); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (ObjectTaggingResult, error) {
		// GCS merges metadata map entries. Never replay the snapshot's receipt,
		// encryption fields or customer metadata onto a concurrent replacement.
		out, err := p.store.UpdateObjectMetadata(ctx, bucket, key, object.Version, object.MetaVersion, map[string]string{ReservedObjectTagsMetadataKey: encoded})
		if err != nil {
			cause := normalizeGCS(err)
			if status := gcsStatusCode(err); status >= 400 && status < 500 && status != http.StatusRequestTimeout {
				cause = errors.Join(ErrWriteRejected, cause)
			}
			return ObjectTaggingResult{}, cause
		}
		if out.Key != key || out.Version != object.Version || out.MetaVersion <= object.MetaVersion || out.Metadata[ReservedObjectTagsMetadataKey] != encoded {
			return ObjectTaggingResult{}, ErrUnavailable
		}
		return ObjectTaggingResult{Tags: maps.Clone(tags)}, nil
	}, nil
}
