package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

func isGCSProvider(p Provider) bool { _, ok := p.(*GCS); return ok }
func nativeGCSDeletion(j state.ObjectDeletion) bool {
	return j.ProviderStatus == "GCS_Enabled" || j.ProviderStatus == "GCS_Suspended"
}

func (s DeletionService) observeGCSDeletionVersioning(ctx context.Context, b state.ObjectBucket, p *GCS) error {
	store, ok := s.Store.(state.ObjectBucketVersioningStore)
	if !ok {
		return ErrUnsupported
	}
	if err := s.before(ctx); err != nil {
		return err
	}
	v, err := p.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return err
	}
	_, err = store.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, v.Status)
	return err
}

func (s DeletionService) prepareGCSDeletion(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion) ([]string, string, error) {
	p, ok := s.Provider.(*GCS)
	if !ok || j.Selector != "" {
		return nil, j.ProviderStatus, ErrConfiguration
	}
	if err := s.before(ctx); err != nil {
		return nil, j.ProviderStatus, err
	}
	v, err := p.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return nil, j.ProviderStatus, err
	}
	if "GCS_"+v.Status != j.ProviderStatus {
		return nil, j.ProviderStatus, ErrConflict
	}
	if err = s.before(ctx); err != nil {
		return nil, j.ProviderStatus, err
	}
	object, err := p.store.ObjectState(ctx, b.PhysicalName, j.Key)
	if errors.Is(normalizeGCS(err), ErrNotFound) {
		return []string{strings.Repeat("0", 64)}, j.ProviderStatus, nil
	}
	if err != nil {
		return nil, j.ProviderStatus, normalizeGCS(err)
	}
	if object.Key != j.Key || object.Version <= 0 {
		return nil, j.ProviderStatus, ErrUnavailable
	}
	// Baselines are private 32-byte hex values in the existing journal. GCS
	// captures exactly one generation instead of hashes of S3 delete markers.
	return []string{fmt.Sprintf("%064x", object.Version)}, j.ProviderStatus, nil
}

func (p *GCS) deleteCapturedCurrent(ctx context.Context, bucket, key string, baseline []string) error {
	if len(baseline) != 1 || len(baseline[0]) != 64 || strings.Trim(baseline[0][:48], "0") != "" {
		return ErrInvalid
	}
	generation, err := strconv.ParseInt(strings.TrimLeft(baseline[0], "0"), 16, 64)
	if strings.Trim(baseline[0], "0") == "" {
		return nil
	}
	if err != nil || generation <= 0 {
		return ErrInvalid
	}
	headers := http.Header{"x-goog-if-generation-match": {strconv.FormatInt(generation, 10)}}
	response, err := p.gcsStreamRequest(ctx, http.MethodDelete, bucket, key, nil, headers, nil, 0)
	if err != nil {
		// A captured generation is no longer current. The condition also
		// prevents an outstanding original request from deleting newer data.
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrPreconditionFailed) {
			return nil
		}
		if errors.Is(err, ErrWriteRejected) {
			return errors.Join(ErrDeletionRejected, err)
		}
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return ErrUnavailable
	}
	return nil
}

var _ ObjectVersionDeleter = (*GCS)(nil)

func (p *GCS) DeleteObjectVersion(ctx context.Context, bucket, key, version string) (VersionDeleteResult, error) {
	if _, err := gcsGeneration(version); err != nil || bucket == "" || !ValidKey(key) {
		return VersionDeleteResult{}, ErrInvalid
	}
	response, err := p.gcsStreamRequest(ctx, http.MethodDelete, bucket, key, url.Values{"generation": {version}}, nil, nil, 0)
	if errors.Is(err, ErrNotFound) {
		return VersionDeleteResult{}, nil
	}
	if err != nil {
		if errors.Is(err, ErrWriteRejected) {
			err = errors.Join(ErrDeletionRejected, err)
		}
		return VersionDeleteResult{}, err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return VersionDeleteResult{}, ErrUnavailable
	}
	return VersionDeleteResult{}, nil
}
