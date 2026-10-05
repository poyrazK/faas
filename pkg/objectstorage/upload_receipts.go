package objectstorage

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// GET receipts remains available with upload policy or the hot flag disabled.
// The same authenticated principal must own the receipt, including non-idempotent uploads.
func (h *uploadHandler) serveUploadReceipt(w http.ResponseWriter, r *http.Request) bool {
	routePath, id, ok := strings.Cut(r.URL.Path, "/receipts/")
	if !ok {
		return false
	}
	name, ok := uploadRouteName(routePath)
	if !ok {
		return false
	}
	app, err := h.lookupApp(r)
	if err != nil {
		h.next.ServeHTTP(w, r)
		return true
	}
	route, err := h.routes.GetObjectUploadRoute(r.Context(), app.AccountID, app.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		h.next.ServeHTTP(w, r)
		return true
	}
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "upload route unavailable")
		return true
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		uploadProblem(w, http.StatusMethodNotAllowed, "upload receipts only accept GET")
		return true
	}
	acct, key, ok := h.authenticate(w, r, app)
	if !ok {
		return true
	}
	h.writeUploadReceipt(w, r, app, route, keySubject(acct, key), id)
	return true
}

func (h *uploadHandler) writeUploadReceipt(w http.ResponseWriter, r *http.Request, app state.App, route state.ObjectUploadRoute, subject, id string) {
	if _, err := uuid.Parse(id); err != nil {
		uploadProblem(w, http.StatusNotFound, "upload receipt not found")
		return
	}
	st, ok := h.routes.(state.ObjectTrackedUploadStore)
	if !ok {
		uploadProblem(w, http.StatusServiceUnavailable, "upload tracking is unavailable")
		return
	}
	c, err := st.GetObjectUploadReceipt(r.Context(), app.AccountID, app.ID, route.ID, subject, id)
	if errors.Is(err, state.ErrNotFound) {
		uploadProblem(w, http.StatusNotFound, "upload receipt not found")
		return
	}
	if err != nil {
		uploadProblem(w, http.StatusServiceUnavailable, "upload receipt unavailable")
		return
	}
	writeUploadJSON(w, http.StatusOK, uploadResponse(c))
}
