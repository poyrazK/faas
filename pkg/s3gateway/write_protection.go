package s3gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func objectCreationRequest(r *http.Request) bool {
	q := operationQuery(r.URL.Query())
	q.Del("x-id")
	return r.Method == http.MethodPut && len(q) == 0 || r.Method == http.MethodPost && queryKeysOnly(q, "uploads") && len(q["uploads"]) == 1 && q.Get("uploads") == ""
}
func supportedWriteProtectionHeader(r *http.Request, name string) bool {
	name = strings.ToLower(name)
	return objectCreationRequest(r) && (name == "x-amz-object-lock-mode" || name == "x-amz-object-lock-retain-until-date" || name == "x-amz-object-lock-legal-hold")
}
func writeProtectionFromHeaders(r *http.Request, signed string) (api.ObjectWriteProtection, error) {
	values := map[string]string{}
	for name, fields := range r.Header {
		if !supportedWriteProtectionHeader(r, name) {
			continue
		}
		lower := strings.ToLower(name)
		if len(fields) != 1 || fields[0] == "" || values[lower] != "" || !strings.Contains(";"+signed+";", ";"+lower+";") {
			return api.ObjectWriteProtection{}, objectstorage.ErrInvalid
		}
		values[lower] = fields[0]
	}
	var p api.ObjectWriteProtection
	mode, date := values["x-amz-object-lock-mode"], values["x-amz-object-lock-retain-until-date"]
	if mode != "" || date != "" {
		until, err := time.Parse(time.RFC3339Nano, date)
		if err != nil {
			return p, objectstorage.ErrInvalid
		}
		p.Retention = &api.ObjectVersionRetention{Mode: mode, RetainUntilDate: &until}
	}
	if hold := values["x-amz-object-lock-legal-hold"]; hold != "" {
		p.LegalHold = &api.ObjectVersionLegalHold{Status: hold}
	}
	p = p.ForWrite()
	if !p.Valid() {
		return p, objectstorage.ErrInvalid
	}
	return p, nil
}
func (h *Handler) captureWriteProtection(w http.ResponseWriter, r *http.Request, req *requestContext) bool {
	selection, err := writeProtectionFromHeaders(r, req.signature.SignedHeader)
	if err == nil && !selection.Empty() && req.credential.URL == nil && (!req.objectLockConfig.Enabled || !objectstorage.SupportsNativeObjectLock(req.provider)) {
		err = objectstorage.ErrUnsupported
	}
	if err == nil && objectCreationRequest(r) && req.credential.URL == nil {
		if st, ok := h.store.(state.ObjectBucketObjectLockStore); ok {
			j, e := st.GetObjectBucketObjectLock(r.Context(), req.bucket.AccountID, req.bucket.AppID, req.bucket.ID)
			if e != nil {
				err = e
			} else if j.EnabledRequired && !req.objectLockConfig.Enabled {
				err = objectstorage.ErrUnsupported
			}
		}
	}
	if err != nil {
		h.providerError(w, r, *req, err, req.signatureKey(r))
		return false
	}
	req.protection = state.ObjectWriteProtectionSnapshot{Requested: selection}
	return true
}
func (h *Handler) protectionContext(ctx context.Context, req requestContext, p state.ObjectWriteProtectionSnapshot) (context.Context, error) {
	return objectstorage.WithObjectWriteProtection(ctx, req.provider, p, func(ctx context.Context) error {
		if h.requestMetrics == nil {
			return objectstorage.ErrConfiguration
		}
		return h.requestMetrics.RecordObjectStorageProviderRequest(ctx, req.bucket.ID, h.now().UTC())
	})
}
