package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// DomainRoute is a durable alias, not a copy of release intent. The Worker
// always checks the canonical site's current durable head as well.
type DomainRoute struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	Hostname          string `json:"hostname"`
	SiteID            string `json:"site_id"`
	CanonicalHostname string `json:"canonical_hostname"`
	OriginID          string `json:"origin_id"`
	AccessAUD         string `json:"access_aud"`
	Generation        int64  `json:"generation"`
	Enabled           bool   `json:"enabled"`
	Deleted           bool   `json:"deleted"`
}

type DomainRoutePublisher interface {
	PutDomain(context.Context, DomainRoute) error
}

func DomainRouteKey(host string) string { return "hosting/domains/" + host + ".json" }

func validDomainRoute(r DomainRoute) bool {
	host, err := NormalizeDomain(r.Hostname, nil)
	canonical := Route{Version: 2, Generation: 1, Revision: 1, SiteID: r.SiteID, Hostname: r.CanonicalHostname, OriginID: r.OriginID}
	return err == nil && host == r.Hostname && r.Version == 1 && uuid.Validate(r.ID) == nil && validDurableRoute(canonical) &&
		r.Generation > 0 && r.Generation <= (1<<53)-1 && (!r.Deleted || !r.Enabled) && (r.AccessAUD == "" || ValidDigest(r.AccessAUD)) && (!r.Enabled || ValidDigest(r.AccessAUD))
}

func (b *R2) PutDomain(ctx context.Context, route DomainRoute) error {
	if !validDomainRoute(route) || route.OriginID != b.ID() {
		return errors.New("invalid hosting domain head")
	}
	data, _ := json.Marshal(route)
	key := DomainRouteKey(route.Hostname)
	for range api.HostingRouteCASAttempts {
		old, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
		exists := err == nil
		if err != nil && !s3ErrorCode(err, "NoSuchKey") && !s3ErrorCode(err, "NotFound") {
			return errors.New("domain head read unavailable")
		}
		var etag *string
		if exists {
			body, readErr := io.ReadAll(io.LimitReader(old.Body, api.HostingDurableRouteMaxBytes+1))
			_ = old.Body.Close()
			var previous DomainRoute
			if readErr != nil || len(body) > api.HostingDurableRouteMaxBytes || json.Unmarshal(body, &previous) != nil || !validDomainRoute(previous) || old.ETag == nil || *old.ETag == "" {
				return errors.New("invalid previous domain head")
			}
			if previous.ID != route.ID || previous.Hostname != route.Hostname || previous.SiteID != route.SiteID || previous.OriginID != route.OriginID || previous.CanonicalHostname != route.CanonicalHostname || previous.Generation > route.Generation || previous.Deleted && !route.Deleted {
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
			return errors.New("domain head publication unavailable")
		}
	}
	return errors.New("domain head publication contended")
}
