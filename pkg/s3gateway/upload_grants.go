package s3gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const UploadGrantQueryParameter = "gregale-upload"

func GenerateUploadGrantToken() (string, string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	return token, UploadGrantTokenHash(token), nil
}

func UploadGrantTokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func UploadGrantURL(endpoint, bucket, key, token string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return "", objectstorage.ErrConfiguration
	}
	u.Path = "/" + bucket + "/" + key
	u.RawPath = ""
	u.RawQuery = url.Values{UploadGrantQueryParameter: []string{token}}.Encode()
	return u.String(), nil
}

func (h *Handler) serveUploadGrant(w http.ResponseWriter, r *http.Request, requestID string) {
	store, ok := h.store.(state.ObjectUploadGrantStore)
	if !ok {
		writeS3Error(w, 503, "ServiceUnavailable", "Upload grants are unavailable.", r.URL.Path, requestID)
		return
	}
	query := r.URL.Query()
	tokens := query[UploadGrantQueryParameter]
	if len(query) != 1 || len(tokens) != 1 || len(tokens[0]) != 43 || r.Header.Get("Authorization") != "" {
		writeS3Error(w, 403, "AccessDenied", "The upload grant is invalid.", r.URL.Path, requestID)
		return
	}
	g, err := store.ResolveObjectUploadGrant(r.Context(), UploadGrantTokenHash(tokens[0]))
	if err != nil {
		h.uploadGrantError(w, r, requestID, err)
		return
	}
	if !validUploadGrantRequest(r, g) || !g.ExpiresAt.After(h.now()) {
		writeS3Error(w, 403, "AccessDenied", "The request does not match this upload grant.", r.URL.Path, requestID)
		return
	}
	backend, err := h.registry.Resolve(g.Bucket.BackendID, g.Bucket.BackendFingerprint)
	if err != nil {
		h.uploadGrantError(w, r, requestID, objectstorage.ErrUnavailable)
		return
	}
	req := requestContext{requestID: requestID, bucket: g.Bucket, provider: backend.Provider, signature: sigV4Request{PayloadHash: "UNSIGNED-PAYLOAD"},
		credential: state.ObjectS3Credential{AccountID: g.Bucket.AccountID, BucketID: g.Bucket.ID, Permission: state.ObjectBucketPermissionWrite}}
	if g.Kind == state.ObjectUploadGrantPut {
		h.upload(w, r, req, g.Key)
		return
	}
	h.serveUploadGrantPart(w, r, req, g)
}

func validUploadGrantRequest(r *http.Request, g state.ObjectUploadGrant) bool {
	name, key, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil || !hasBucket || !hasKey || name != g.Bucket.Name || key != g.Key || r.Method != http.MethodPut || r.ContentLength != g.SizeBytes ||
		len(r.TransferEncoding) != 0 || r.Header.Get("X-Amz-Copy-Source") != "" || hasUnsupportedS3Semantics(r) {
		return false
	}
	for name, values := range r.Header {
		if !uploadMetadataHeader(name) || g.Kind == state.ObjectUploadGrantMultipartPart && name == "Content-Type" {
			continue
		}
		if len(values) != 1 || g.Headers[name] != values[0] {
			return false
		}
	}
	for name, value := range g.Headers {
		if name != "Content-Length" && r.Header.Get(name) != value {
			return false
		}
	}
	return g.Headers["Content-Length"] == strconv.FormatInt(r.ContentLength, 10)
}

func uploadMetadataHeader(name string) bool {
	switch name {
	case "Content-Type", "Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "X-Amz-Tagging":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "x-amz-meta-")
}

func (h *Handler) uploadGrantError(w http.ResponseWriter, r *http.Request, id string, err error) {
	if errors.Is(err, state.ErrNotFound) {
		writeS3Error(w, 403, "AccessDenied", "The upload grant is invalid or expired.", r.URL.Path, id)
		return
	}
	writeS3Error(w, 503, "ServiceUnavailable", "Gregale could not resolve this upload grant.", r.URL.Path, id)
}

func (h *Handler) serveUploadGrantPart(w http.ResponseWriter, r *http.Request, req requestContext, g state.ObjectUploadGrant) {
	if h.multipartStore == nil {
		h.uploadGrantError(w, r, req.requestID, objectstorage.ErrUnavailable)
		return
	}
	u, err := h.multipartStore.GetObjectMultipartUpload(r.Context(), g.Bucket.AccountID, g.Bucket.AppID, g.Bucket.ID, g.UploadID)
	if err != nil || u.State != state.ObjectMultipartActive || u.ProviderUploadID != g.ProviderUploadID || u.Key != g.Key || !u.ExpiresAt.After(h.now()) {
		h.uploadGrantError(w, r, req.requestID, state.ErrNotFound)
		return
	}
	h.proxyMultipartPart(w, r, req, u, g.PartNumber)
}
