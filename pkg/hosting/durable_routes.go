package hosting

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrStaleRoute = errors.New("hosting route generation is stale or conflicts with a retained deletion")

// RouteKey is outside both grant-authorized staging and collectible published
// releases. In particular, deletion heads must outlive the SQL site catalog.
func RouteKey(hostname string) string { return "hosting/routes/" + hostname + ".json" }

func validDurableRoute(r Route) bool {
	if r.Version != 2 || r.Generation < 1 || r.Generation > (1<<53)-1 || r.ExpiresAt != 0 || r.Revision < 1 || r.Revision > (1<<53)-1 || uuid.Validate(r.SiteID) != nil || !strings.HasPrefix(r.Hostname, r.SiteID+".") || len(r.Hostname) > 253 {
		return false
	}
	for _, label := range strings.Split(r.Hostname, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	origin, err := hex.DecodeString(r.OriginID)
	if err != nil || len(origin) != 32 || strings.ToLower(r.OriginID) != r.OriginID || len(r.ReadyReleases) > api.HostingReleasesPerSite || !sort.StringsAreSorted(r.ReadyReleases) || r.Deleted && r.Enabled {
		return false
	}
	active := false
	for i, id := range r.ReadyReleases {
		if uuid.Validate(id) != nil || i > 0 && id == r.ReadyReleases[i-1] {
			return false
		}
		active = active || id == r.ActiveReleaseID
	}
	return !r.Enabled || active
}

// Put fences every write with R2's atomic If-Match / If-None-Match conditions.
// A request that commits after its caller times out cannot overwrite a newer
// head: its old ETag no longer matches. Same-generation retries perform no PUT.
func (b *R2) Put(ctx context.Context, route Route) error {
	if !validDurableRoute(route) || route.OriginID != b.ID() {
		return errors.New("invalid durable hosting route")
	}
	data, err := json.Marshal(route)
	if err != nil || len(data) > api.HostingDurableRouteMaxBytes {
		return errors.New("invalid durable hosting route size")
	}
	key := RouteKey(route.Hostname)
	for range api.HostingRouteCASAttempts {
		previous, etag, exists, readErr := b.readRoute(ctx, key)
		if readErr != nil {
			return readErr
		}
		if exists {
			if previous.SiteID != route.SiteID || previous.OriginID != route.OriginID || previous.Hostname != route.Hostname || previous.Generation > route.Generation || previous.Deleted && !route.Deleted {
				return ErrStaleRoute
			}
			if previous.Generation == route.Generation {
				canonical, _ := json.Marshal(previous)
				if !bytes.Equal(canonical, data) {
					return ErrStaleRoute
				}
				return nil
			}
		}
		input := &s3.PutObjectInput{Bucket: &b.bucket, Key: &key, Body: bytes.NewReader(data), ContentLength: ptr(int64(len(data))), ContentType: ptr("application/json"), CacheControl: ptr("no-store")}
		if exists {
			input.IfMatch = &etag
		} else {
			input.IfNoneMatch = ptr("*")
		}
		if _, err = b.client.PutObject(ctx, input); err == nil {
			return nil
		}
		if !s3ErrorCode(err, "PreconditionFailed") && !s3ErrorCode(err, "ConditionalRequestConflict") {
			return errors.New("durable hosting route publication unavailable")
		}
	}
	return errors.New("durable hosting route publication contended; retry")
}

func ptr[T any](value T) *T { return &value }

func s3ErrorCode(err error, code string) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == code
}

func (b *R2) readRoute(ctx context.Context, key string) (Route, string, bool, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
	if s3ErrorCode(err, "NoSuchKey") || s3ErrorCode(err, "NotFound") {
		return Route{}, "", false, nil
	}
	if err != nil {
		return Route{}, "", false, errors.New("durable hosting route read unavailable")
	}
	defer func() { _ = out.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(out.Body, api.HostingDurableRouteMaxBytes+1))
	var route Route
	if err != nil || len(data) > api.HostingDurableRouteMaxBytes || json.Unmarshal(data, &route) != nil || !validDurableRoute(route) || out.ETag == nil || *out.ETag == "" {
		return Route{}, "", false, errors.New("invalid durable hosting route head")
	}
	return route, *out.ETag, true, nil
}
