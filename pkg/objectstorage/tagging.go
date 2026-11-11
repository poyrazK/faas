package objectstorage

import (
	"context"
	"maps"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type TaggingService struct {
	References state.ObjectVersionReferenceStore
	Provider   Provider
	// BeforeOperation admits one customer operation; BeforeRequest reserves each native RPC.
	BeforeOperation func(context.Context) error
	BeforeRequest   func(context.Context) error
	// Mutate records a writer after preparation and before native mutation.
	Mutate func(context.Context, func(context.Context) (api.ObjectTaggingResult, error)) (api.ObjectTaggingResult, error)
}

type objectTaggingPreparer interface {
	prepareObjectTagging(context.Context, string, string, string, map[string]string, func(context.Context) error) (func(context.Context) (ObjectTaggingResult, error), error)
}

func ValidateObjectTaggingRequest(r *http.Request) error {
	for _, name := range []string{"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "X-Amz-Copy-Source", "X-Amz-Tagging", "X-Amz-Metadata-Directive", "X-Amz-Tagging-Directive"} {
		if len(r.Header.Values(name)) != 0 {
			return ErrUnsupported
		}
	}
	if r.Method != http.MethodPut && r.ContentLength != 0 {
		return ErrInvalid
	}
	return nil
}

func (s TaggingService) Do(ctx context.Context, b state.ObjectBucket, method, key, selector string, tags map[string]string) (api.ObjectTaggingResult, error) {
	result := api.ObjectTaggingResult{Tags: map[string]string{}}
	if !ValidKey(key) || selector != "" && !state.ValidObjectVersionID(selector) || method != http.MethodGet && method != http.MethodPut && method != http.MethodDelete || ValidateObjectMetadata(ObjectMetadata{Tags: tags}) != nil {
		return result, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectTaggingOperationTimeout)
	defer cancel()
	native := ""
	if selector != "" {
		if s.References == nil {
			return result, ErrUnsupported
		}
		var err error
		native, err = s.References.ResolveObjectVersion(ctx, b.AccountID, b.ID, key, selector)
		if err != nil {
			return result, err
		}
		if !validNativeVersionID(native) || (selector == "null") != (native == "null") {
			return result, ErrUnavailable
		}
	}
	versioned, capable := s.Provider.(ObjectVersionTagger)
	legacy, fallback := s.Provider.(ObjectTagger)
	if !capable && (!fallback || selector != "") || capable && s.References == nil {
		return result, ErrUnsupported
	}
	if s.BeforeOperation != nil {
		if err := s.BeforeOperation(ctx); err != nil {
			return result, err
		}
	}
	call, err := s.prepare(ctx, b, method, key, native, tags, versioned, legacy)
	if err != nil {
		return result, err
	}
	finish := func(ctx context.Context) (api.ObjectTaggingResult, error) {
		out, err := call(ctx)
		if err != nil {
			return result, err
		}
		return s.finish(ctx, b, key, selector, native, out)
	}
	if method != http.MethodGet && s.Mutate != nil {
		return s.Mutate(ctx, finish)
	}
	return finish(ctx)
}

func (s TaggingService) prepare(ctx context.Context, b state.ObjectBucket, method, key, native string, tags map[string]string, versioned ObjectVersionTagger, legacy ObjectTagger) (func(context.Context) (ObjectTaggingResult, error), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p, ok := s.Provider.(objectTaggingPreparer); ok {
		return p.prepareObjectTagging(ctx, b.PhysicalName, method, key, tags, s.BeforeRequest)
	}
	if s.BeforeRequest != nil {
		if err := s.BeforeRequest(ctx); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (ObjectTaggingResult, error) {
		if versioned != nil {
			return dispatchVersionTags(ctx, versioned, b.PhysicalName, method, key, native, tags)
		}
		return dispatchLegacyTags(ctx, legacy, b.PhysicalName, method, key, tags)
	}, nil
}

func (s TaggingService) finish(ctx context.Context, b state.ObjectBucket, key, selector, native string, out ObjectTaggingResult) (api.ObjectTaggingResult, error) {
	result := api.ObjectTaggingResult{Tags: map[string]string{}}
	if ValidateObjectMetadata(ObjectMetadata{Tags: out.Tags}) != nil || native != "" && out.ProviderVersionID != native || out.ProviderVersionID != "" && !validNativeVersionID(out.ProviderVersionID) {
		return result, ErrUnavailable
	}
	if out.ProviderVersionID != "" {
		finish, stop := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectTaggingOperationTimeout)
		defer stop()
		refs, e := s.References.RecordObjectVersions(finish, b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: key, ProviderVersionID: out.ProviderVersionID}})
		if e != nil || len(refs) != 1 || refs[0].Key != key || refs[0].ProviderVersionID != out.ProviderVersionID || !state.ValidObjectVersionID(refs[0].ID) || (refs[0].ID == "null") != (out.ProviderVersionID == "null") || selector != "" && refs[0].ID != selector {
			return result, ErrUnavailable
		}
		result.VersionID = refs[0].ID
	}
	if out.Tags != nil {
		result.Tags = maps.Clone(out.Tags)
	}
	return result, nil
}

func dispatchVersionTags(ctx context.Context, p ObjectVersionTagger, bucket, method, key, native string, tags map[string]string) (ObjectTaggingResult, error) {
	switch method {
	case http.MethodGet:
		return p.GetObjectVersionTags(ctx, bucket, key, native)
	case http.MethodPut:
		out, err := p.PutObjectVersionTags(ctx, bucket, key, native, tags)
		out.Tags = maps.Clone(tags)
		return out, err
	default:
		out, err := p.DeleteObjectVersionTags(ctx, bucket, key, native)
		out.Tags = nil
		return out, err
	}
}

func dispatchLegacyTags(ctx context.Context, p ObjectTagger, bucket, method, key string, tags map[string]string) (ObjectTaggingResult, error) {
	var out ObjectTaggingResult
	var err error
	switch method {
	case http.MethodGet:
		out.Tags, err = p.GetObjectTags(ctx, bucket, key)
	case http.MethodPut:
		err = p.PutObjectTags(ctx, bucket, key, tags)
		out.Tags = maps.Clone(tags)
	default:
		err = p.DeleteObjectTags(ctx, bucket, key)
	}
	return out, err
}
