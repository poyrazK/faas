package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PreviewRoute is immutable release intent with a terminal revocation fence.
type PreviewRoute struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	Hostname          string `json:"hostname"`
	SiteID            string `json:"site_id"`
	CanonicalHostname string `json:"canonical_hostname"`
	OriginID          string `json:"origin_id"`
	ReleaseID         string `json:"release_id"`
	ExpiresAt         int64  `json:"expires_at"`
	Generation        int64  `json:"generation"`
	Revoked           bool   `json:"revoked"`
}

type PreviewRoutePublisher interface {
	PutPreview(context.Context, PreviewRoute) error
}

func PreviewRouteKey(host string) string { return "hosting/previews/" + host + ".json" }

func validPreviewRoute(r PreviewRoute) bool {
	canonical := Route{Version: 2, Generation: 1, Revision: 1, SiteID: r.SiteID, Hostname: r.CanonicalHostname, OriginID: r.OriginID}
	return r.Version == 1 && uuid.Validate(r.ID) == nil && uuid.Validate(r.ReleaseID) == nil && validDurableRoute(canonical) &&
		r.Hostname == "p-"+r.ID+strings.TrimPrefix(r.CanonicalHostname, r.SiteID) && len(r.Hostname) <= 253 &&
		r.ExpiresAt > 0 && r.ExpiresAt <= (1<<53)-1 && (r.Generation == 1 && !r.Revoked || r.Generation == 2 && r.Revoked)
}

func (b *R2) PutPreview(ctx context.Context, route PreviewRoute) error {
	if !validPreviewRoute(route) || route.OriginID != b.ID() {
		return errors.New("invalid hosting preview head")
	}
	data, _ := json.Marshal(route)
	key := PreviewRouteKey(route.Hostname)
	for range api.HostingRouteCASAttempts {
		old, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
		exists := err == nil
		if err != nil && !s3ErrorCode(err, "NoSuchKey") && !s3ErrorCode(err, "NotFound") {
			return errors.New("preview head read unavailable")
		}
		var etag *string
		if exists {
			body, readErr := io.ReadAll(io.LimitReader(old.Body, api.HostingDurableRouteMaxBytes+1))
			_ = old.Body.Close()
			var previous PreviewRoute
			if readErr != nil || len(body) > api.HostingDurableRouteMaxBytes || json.Unmarshal(body, &previous) != nil || !validPreviewRoute(previous) || old.ETag == nil || *old.ETag == "" {
				return errors.New("invalid previous preview head")
			}
			if previous.ID != route.ID || previous.Hostname != route.Hostname || previous.SiteID != route.SiteID || previous.OriginID != route.OriginID || previous.CanonicalHostname != route.CanonicalHostname || previous.Generation > route.Generation || previous.Revoked && !route.Revoked || previous.ReleaseID != route.ReleaseID || previous.ExpiresAt != route.ExpiresAt {
				return ErrStaleRoute
			}
			if previous.Generation == route.Generation {
				canonical, _ := json.Marshal(previous)
				if !bytes.Equal(canonical, data) {
					return ErrStaleRoute
				}
				return nil
			}
			etag = old.ETag
		}
		input := &s3.PutObjectInput{Bucket: &b.bucket, Key: &key, Body: bytes.NewReader(data), ContentLength: ptr(int64(len(data))), ContentType: ptr("application/json"), CacheControl: ptr("no-store")}
		if exists {
			input.IfMatch = etag
		} else {
			input.IfNoneMatch = ptr("*")
		}
		if _, err = b.client.PutObject(ctx, input); err == nil {
			return nil
		}
		if !s3ErrorCode(err, "PreconditionFailed") && !s3ErrorCode(err, "ConditionalRequestConflict") {
			return errors.New("preview head publication unavailable")
		}
	}
	return errors.New("preview head publication contended")
}
