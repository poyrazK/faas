package s3gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func supportedEncryptionHeader(r *http.Request, name string) bool {
	switch strings.ToLower(name) {
	case "x-amz-server-side-encryption", "x-amz-server-side-encryption-aws-kms-key-id", "x-amz-server-side-encryption-context", "x-amz-server-side-encryption-bucket-key-enabled":
		query := operationQuery(r.URL.Query())
		query.Del("x-id")
		return r.Method == http.MethodPut && len(query) == 0 || r.Method == http.MethodPost && queryKeysOnly(query, "uploads") && len(query["uploads"]) == 1 && query.Get("uploads") == ""
	default:
		return false
	}
}

func encryptionFromHeaders(r *http.Request, signed string) (api.ObjectEncryption, error) {
	values := map[string]string{}
	for name, fields := range r.Header {
		if !supportedEncryptionHeader(r, name) {
			continue
		}
		lower := strings.ToLower(name)
		if len(fields) != 1 || fields[0] == "" || values[lower] != "" || !strings.Contains(";"+signed+";", ";"+lower+";") {
			return api.ObjectEncryption{}, objectstorage.ErrInvalid
		}
		values[lower] = fields[0]
	}
	e := api.ObjectEncryption{Algorithm: values["x-amz-server-side-encryption"], KeyID: values["x-amz-server-side-encryption-aws-kms-key-id"], Context: values["x-amz-server-side-encryption-context"]}
	if raw := values["x-amz-server-side-encryption-bucket-key-enabled"]; raw != "" {
		if raw != "true" && raw != "false" {
			return e, objectstorage.ErrInvalid
		}
		value := raw == "true"
		e.BucketKeyEnabled = &value
	}
	if !e.Valid() {
		return e, objectstorage.ErrInvalid
	}
	return e, nil
}

func (h *Handler) captureEncryption(w http.ResponseWriter, r *http.Request, req *requestContext) bool {
	selection, err := encryptionFromHeaders(r, req.signature.SignedHeader)
	if err == nil && !selection.Empty() {
		account, parseErr := uuid.Parse(req.bucket.AccountID)
		if parseErr != nil {
			err = objectstorage.ErrConfiguration
		} else {
			req.encryption, err = req.encryptionConfig.Resolve(account.String(), selection)
		}
		if _, capable := req.provider.(objectstorage.ObjectEncryptionProvider); err == nil && !capable {
			err = objectstorage.ErrUnsupported
		}
	}
	if err != nil {
		h.providerError(w, r, *req, err, req.signatureKey(r))
		return false
	}
	return true
}

func writeEncryptionHeaders(headers http.Header, e api.ObjectEncryption) {
	if e.Empty() {
		return
	}
	headers.Set("X-Amz-Server-Side-Encryption", e.Algorithm)
	if e.KeyID != "" {
		headers.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", e.KeyID)
	}
	if e.BucketKeyEnabled != nil {
		headers.Set("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled", strconv.FormatBool(*e.BucketKeyEnabled))
	}
}

func (h *Handler) encryptionContext(ctx context.Context, req requestContext) context.Context {
	return objectstorage.WithEncryptionRequestRecorder(ctx, func(ctx context.Context) error {
		if h.requestMetrics == nil {
			return objectstorage.ErrConfiguration
		}
		return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
	})
}

func (h *Handler) ensureCapturedMultipart(ctx context.Context, req requestContext, u state.ObjectMultipartUpload) (string, error) {
	var err error
	ctx, err = h.protectionContext(ctx, req, u.Protection)
	if err != nil {
		return "", err
	}
	r := objectstorage.MultipartCreateRequest{SessionID: u.ID, Key: u.Key, SizeBytes: u.SizeBytes,
		Metadata: objectstorage.ObjectMetadata{ContentType: u.ContentType, CacheControl: u.Metadata.CacheControl,
			ContentDisposition: u.Metadata.ContentDisposition, ContentEncoding: u.Metadata.ContentEncoding,
			ContentLanguage: u.Metadata.ContentLanguage, Metadata: u.Metadata.UserMetadata, Tags: u.Metadata.Tags}}
	if h.requestMetrics == nil && !u.Encryption.Empty() {
		return "", objectstorage.ErrConfiguration
	}
	before := func(ctx context.Context) error {
		if h.requestMetrics == nil {
			return ctx.Err()
		}
		return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
	}
	if u.Encryption.Empty() {
		if err := before(ctx); err != nil {
			return "", err
		}
	} else {
		r.BeforeRequest = before
		ctx = h.encryptionContext(ctx, req)
	}
	return objectstorage.EnsureMultipartWithEncryption(ctx, req.provider, req.bucket.PhysicalName, r, u.Encryption)
}

func (h *Handler) multipartPartEncryption(w http.ResponseWriter, r *http.Request, req requestContext, u state.ObjectMultipartUpload, headers http.Header) bool {
	if u.Encryption.Empty() {
		return true
	}
	// Some compatible providers omit these optional part response headers. The
	// completion still requires exact object proof; supplied headers must agree.
	present := false
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-server-side-encryption") {
			present = true
		}
	}
	if !present {
		return true
	}
	selection, err := objectstorage.VerifyEncryptionAcknowledgment(headers, u.Encryption)
	if err != nil {
		h.providerError(w, r, req, err, u.Key)
		return false
	}
	writeEncryptionHeaders(w.Header(), selection)
	return true
}
