// Package runartifacts promotes verified Run outputs into app-owned storage.
package runartifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	state.ObjectUploadRouteStore
	state.ObjectBucketStore
	objectstorage.RouteDeliveryStore
}

type Access struct {
	AccountID       string
	AppID           string
	AppSlug         string
	AuthorizeBucket func(context.Context, state.ObjectBucket) error
}

type Service struct {
	Store    Store
	Registry *objectstorage.Registry
	Slots    chan struct{}
	TempDir  string
	Enabled  bool
}

// OpenSource must authorize the Run and verify its success, contract verdict and
// exact artifact metadata before opening bytes. It is never called on replay.
type OpenSource func(context.Context, api.DeliverRunArtifactRequest) (io.ReadCloser, error)

func subject(app string) string { return "run-artifact:" + app }

func Normalize(req api.DeliverRunArtifactRequest) (api.DeliverRunArtifactRequest, error) {
	if req.IdempotencyKey == "" || strings.TrimSpace(req.IdempotencyKey) != req.IdempotencyKey || len(req.IdempotencyKey) > api.RunArtifactDeliveryKeyMaxBytes || !utf8.ValidString(req.IdempotencyKey) || strings.IndexFunc(req.IdempotencyKey, unicode.IsControl) >= 0 {
		return req, api.ErrValidation("a bounded, stable idempotency_key is required for delivery")
	}
	if id, err := uuid.Parse(req.ExecutionID); err != nil || id.String() != req.ExecutionID || id == uuid.Nil ||
		!runfile.ValidPath(req.ArtifactName, api.ExecutionArtifactMaxPathBytes) || req.SizeBytes < 0 || req.SizeBytes > api.ExecutionFileHardMaxBytes {
		return req, api.ErrValidation("a canonical execution ID, normalized artifact name and bounded size_bytes are required")
	}
	req.SHA256 = "sha256:" + strings.TrimPrefix(req.SHA256, "sha256:")
	media, params, err := mime.ParseMediaType(req.ContentType)
	if err != nil || !runfile.ValidDigest(req.SHA256) || objectstorage.ValidateContentType(req.ContentType) != nil {
		return req, api.ErrValidation("valid sha256 and content_type are required")
	}
	req.ContentType = mime.FormatMediaType(strings.ToLower(media), params)
	return req, nil
}

func identity(req api.DeliverRunArtifactRequest) (string, string) {
	key := sha256.Sum256([]byte(req.IdempotencyKey))
	raw, _ := json.Marshal(req)
	fingerprint := sha256.Sum256(raw)
	return "run-artifact-v1-" + hex.EncodeToString(key[:]), hex.EncodeToString(fingerprint[:])
}

func (s Service) Deliver(ctx context.Context, access Access, routeName string, request api.DeliverRunArtifactRequest, open OpenSource) (api.RunArtifactDelivery, error) {
	req, err := Normalize(request)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	route, err := s.route(ctx, access, routeName)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	key, fingerprint := identity(req)
	old, err := s.Store.GetObjectUploadIntent(ctx, route.ID, subject(access.AppID), key)
	if err == nil {
		out, authErr := s.viewAuthorized(ctx, access, routeName, old)
		if authErr != nil {
			return api.RunArtifactDelivery{}, authErr
		}
		if old.RequestFingerprint != fingerprint {
			return api.RunArtifactDelivery{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Delivery conflict", "delivery already exists with different artifact metadata")
		}
		return out, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return api.RunArtifactDelivery{}, err
	}
	if !s.Enabled {
		return api.RunArtifactDelivery{}, api.ErrCapacity("new Run artifact deliveries are disabled")
	}
	bucket, err := s.bucket(ctx, access, route.BucketID)
	if err != nil {
		return api.RunArtifactDelivery{}, err
	}
	if err := s.checkDestination(route, bucket, req); err != nil {
		return api.RunArtifactDelivery{}, err
	}
	return s.transfer(ctx, access, routeName, route, bucket, req, key, fingerprint, open)
}

func (s Service) Get(ctx context.Context, access Access, routeName, id string) (api.RunArtifactDelivery, state.ObjectUploadCompletion, error) {
	route, err := s.route(ctx, access, routeName)
	if err != nil {
		return api.RunArtifactDelivery{}, state.ObjectUploadCompletion{}, err
	}
	if parsed, e := uuid.Parse(id); e != nil || parsed.String() != id {
		return api.RunArtifactDelivery{}, state.ObjectUploadCompletion{}, state.ErrNotFound
	}
	c, err := s.Store.GetObjectUploadReceipt(ctx, access.AccountID, access.AppID, route.ID, subject(access.AppID), id)
	if err != nil {
		return api.RunArtifactDelivery{}, c, err
	}
	out, err := s.viewAuthorized(ctx, access, routeName, c)
	return out, c, err
}

func (s Service) route(ctx context.Context, access Access, name string) (state.ObjectUploadRoute, error) {
	if s.Store == nil || access.AccountID == "" || access.AppID == "" || access.AuthorizeBucket == nil {
		return state.ObjectUploadRoute{}, api.ErrCapacity("Run artifact delivery storage is unavailable")
	}
	route, err := s.Store.GetObjectUploadRoute(ctx, access.AccountID, access.AppID, name)
	if err == nil && (route.AccountID != access.AccountID || route.AppID != access.AppID) {
		err = state.ErrNotFound
	}
	return route, err
}

func (s Service) bucket(ctx context.Context, access Access, id string) (state.ObjectBucket, error) {
	bucket, err := s.Store.GetObjectBucket(ctx, access.AccountID, access.AppID, id)
	if err == nil {
		err = access.AuthorizeBucket(ctx, bucket)
	}
	return bucket, err
}

func (s Service) checkDestination(route state.ObjectUploadRoute, bucket state.ObjectBucket, req api.DeliverRunArtifactRequest) error {
	if !route.Enabled || bucket.State != "ready" {
		return state.ErrNotFound
	}
	if s.Registry == nil {
		return api.ErrCapacity("object storage is unavailable")
	}
	limit := min(route.MaxBytes, s.Registry.MaxUploadBytes, s.Registry.MaxSinglePutBytes)
	if req.SizeBytes > limit {
		return api.NewProblem(http.StatusRequestEntityTooLarge, "run_artifact_destination_limit", "Artifact exceeds destination limit", "artifact exceeds the destination upload route or storage transfer limit").WithByteLimit(limit, req.SizeBytes).WithDocs(api.ObjectStorageLimitsDocsURL)
	}
	media, _, _ := mime.ParseMediaType(req.ContentType)
	active := objectstorage.ActiveContentType(media)
	allowed := len(route.AllowedContentTypes) == 0 && !active
	for _, value := range route.AllowedContentTypes {
		value = strings.ToLower(strings.TrimSpace(value))
		allowed = allowed || value == media || !active && strings.HasSuffix(value, "/*") && strings.HasPrefix(media, strings.TrimSuffix(value, "*"))
	}
	if !allowed || objectstorage.PublicServed(bucket) && objectstorage.ActiveContentType(req.ContentType) {
		return api.ErrValidation("artifact content type is not allowed by the destination")
	}
	return nil
}

func (s Service) viewAuthorized(ctx context.Context, access Access, routeName string, c state.ObjectUploadCompletion) (api.RunArtifactDelivery, error) {
	if c.AccountID != access.AccountID || c.AppID != access.AppID || c.SubjectID != subject(access.AppID) {
		return api.RunArtifactDelivery{}, state.ErrNotFound
	}
	if _, err := s.bucket(ctx, access, c.BucketID); err != nil {
		return api.RunArtifactDelivery{}, err
	}
	out := api.RunArtifactDelivery{ID: c.ID, Status: c.Status, SizeBytes: c.Bytes, ContentType: c.ContentType, ErrorCode: state.ViewObjectWriteReceipt(c).ErrorCode, CreatedAt: c.CreatedAt,
		ReceiptURL: "/v1/apps/" + url.PathEscape(access.AppSlug) + "/run-artifact-deliveries/" + url.PathEscape(routeName) + "/" + c.ID}
	if c.Status == "completed" {
		out.Object = &api.RunArtifactObject{BucketID: c.BucketID, Key: c.Key, VersionID: c.VersionID}
	}
	return out, nil
}

func Problem(err error) *api.Problem {
	var p *api.Problem
	if errors.As(err, &p) {
		return p
	}
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Delivery not found", "no such Run artifact delivery or destination")
	}
	if errors.Is(err, state.ErrConflict) || errors.Is(err, objectstorage.ErrConflict) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Delivery conflict", "delivery or destination changed; inspect its receipt")
	}
	return api.ErrCapacity("Run artifact delivery could not be completed")
}

func objectKey(route state.ObjectUploadRoute, idem, name string) string {
	sum := sha256.Sum256([]byte(route.ID + "\x00" + idem))
	return path.Join(route.KeyPrefix, "run-artifacts", hex.EncodeToString(sum[:]), path.Base(name))
}
