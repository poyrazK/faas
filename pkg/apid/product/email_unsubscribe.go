package product

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/state"
)

// GET/HEAD never resolve a capability or change state. Human links keep the
// capability in the fragment, outside access logs and referrer headers.
func (s *Service) EmailUnsubscribePage(w http.ResponseWriter, r *http.Request) {
	content, err := portalFiles.ReadFile("email-unsubscribe.html")
	if err != nil {
		http.Error(w, "Unsubscribe unavailable", http.StatusInternalServerError)
		return
	}
	portalHeaders(w, "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	_, _ = w.Write([]byte(strings.ReplaceAll(string(content), "__REQUEST_TIMEOUT_MS__", strconv.FormatInt(api.ProductControlEmailOwnerMaxDuration.Milliseconds(), 10))))
}
func (s *Service) EmailUnsubscribeScript(w http.ResponseWriter, r *http.Request) {
	servePortalFile(w, r, "email-unsubscribe.js", "text/javascript; charset=utf-8")
}
func (s *Service) EmailUnsubscribe(w http.ResponseWriter, r *http.Request) {
	portalHeaders(w, "application/json")
	// Mailbox one-click POSTs have no Origin. Browser confirmations must use
	// this HTTPS origin; neither path consults cookies or customer sessions.
	if len(r.Header.Values("Origin")) > 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Origin rejected", "Use this HTTPS origin to confirm unsubscribe."))
		return
	}
	if r.Header.Get("Origin") != "" && !customerSameOrigin(w, r) {
		return
	}
	token, ok := decodeEmailUnsubscribe(w, r)
	if !ok {
		return
	}
	hash, err := productemail.UnsubscribeHash(token)
	if err != nil {
		unsubscribeRespond(w, nil, productemail.ErrNotFound)
		return
	}
	store, ok := s.Host.Store().(state.ProductEmailStore)
	if !ok {
		unsubscribeRespond(w, nil, state.ErrProductBindingUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlEmailOwnerMaxDuration)
	defer cancel()
	result, err := store.ProductEmailRepository().Unsubscribe(ctx, hash, time.Now().UTC())
	unsubscribeRespond(w, result, err)
}
func decodeEmailUnsubscribe(w http.ResponseWriter, r *http.Request) (string, bool) {
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType == "multipart/form-data" && (len(params) != 1 || params["boundary"] == "") || contentType != "multipart/form-data" && (len(params) > 1 || len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		unsubscribeRespond(w, nil, productemail.ErrInvalid)
		return "", false
	}
	if contentType == "application/json" && r.URL.RawQuery == "" {
		var body struct {
			Token string `json:"token"`
		}
		ok := operation.DecodeOperationBody(w, r, &body, api.ProductControlEmailUnsubscribeBodyMaxBytes)
		return body.Token, ok
	}
	// RFC 8058: receivers POST the exact field to the List-Unsubscribe URL.
	// A normal GET of that URL still serves only the confirmation page.
	if (contentType == "application/x-www-form-urlencoded" || contentType == "multipart/form-data") && len(r.URL.RawQuery) <= api.ProductControlEmailUnsubscribeBodyMaxBytes {
		query, queryErr := url.ParseQuery(r.URL.RawQuery)
		raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, api.ProductControlEmailUnsubscribeBodyMaxBytes))
		if queryErr == nil && readErr == nil && len(query) == 1 && len(query["token"]) == 1 && emailOneClickBody(raw, params["boundary"]) {
			return query.Get("token"), true
		}
	}
	unsubscribeRespond(w, nil, productemail.ErrInvalid)
	return "", false
}
func emailOneClickBody(raw []byte, boundary string) bool {
	if boundary == "" {
		form, err := url.ParseQuery(string(raw))
		return err == nil && len(form) == 1 && len(form["List-Unsubscribe"]) == 1 && form.Get("List-Unsubscribe") == "One-Click"
	}
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "List-Unsubscribe" || part.FileName() != "" {
		return false
	}
	value, err := io.ReadAll(part)
	if err != nil || string(value) != "One-Click" {
		return false
	}
	_, err = reader.NextPart()
	return errors.Is(err, io.EOF)
}
func unsubscribeRespond(w http.ResponseWriter, value any, err error) {
	if errors.Is(err, productemail.ErrNotFound) || errors.Is(err, productemail.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "product_email_unsubscribe_unavailable", "Unsubscribe link unavailable", "This link has expired or newer preferences were saved. Use a recent email or sign in to manage your current notification preferences."))
		return
	}
	if errors.Is(err, productemail.ErrInvalid) {
		api.WriteProblem(w, api.ErrValidation("Confirm with the unsubscribe link from your email."))
		return
	}
	emailRespond(w, value, err)
}
