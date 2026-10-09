package main

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
)

// production-us hunt #8 (H8-28): the sign-in page apid serves on the API host
// (where `gregale dashboard` and expired dashboard sessions land) linked to
// GET /signup and GET /login/forgot, both 405, and its plain HTML form got the
// JSON body meant for the web console's fetch() calls. These helpers keep that
// JSON contract for API clients and give a browser form post a redirect or a
// re-rendered page instead.

const (
	forgotPasswordPath = "/login/forgot"
	resetRequestSent   = "If that address belongs to a verified account, a reset link is on its way. It expires in 15 minutes."
)

// browserFormPost reports whether r is an HTML form submission from a browser:
// a form-encoded body and an explicit text/html Accept. API clients that post
// forms without asking for HTML keep receiving JSON.
func browserFormPost(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/x-www-form-urlencoded" && api.AcceptsHTML(r)
}

// capturedResponse buffers a JSON auth handler's response so a browser form
// post can be answered with HTML. Only Set-Cookie is forwarded.
type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capturedResponse) Header() http.Header { return c.header }

func (c *capturedResponse) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(b)
}

func (c *capturedResponse) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

// problemMessage is the customer-facing line of a captured problem response.
func (c *capturedResponse) problemMessage() string {
	var p struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(c.body.Bytes(), &p) == nil {
		if p.Detail != "" {
			return strings.ToUpper(p.Detail[:1]) + p.Detail[1:]
		}
		if p.Title != "" {
			return p.Title
		}
	}
	return "Something went wrong. Please try again."
}

// browserAuthForm runs h unchanged for API clients. For a browser form post it
// forwards the session cookie and calls done with the outcome; done writes the
// HTML response.
func browserAuthForm(h http.Handler, done func(w http.ResponseWriter, r *http.Request, captured *capturedResponse)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !browserFormPost(r) {
			h.ServeHTTP(w, r)
			return
		}
		captured := &capturedResponse{header: http.Header{}}
		h.ServeHTTP(captured, r)
		if captured.status == 0 {
			captured.status = http.StatusOK
		}
		for _, cookie := range captured.header.Values("Set-Cookie") {
			w.Header().Add("Set-Cookie", cookie)
		}
		done(w, r, captured)
	})
}

// finishBrowserLogin sends a signed-in browser to its sanitized next page, or
// re-renders the sign-in form with the failure.
func (a *authHandlers) finishBrowserLogin(w http.ResponseWriter, r *http.Request, captured *capturedResponse) {
	next := dashboardMFANext(r.PostFormValue("next"))
	if captured.status < http.StatusBadRequest {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	a.renderLogin(w, r, captured.status, captured.problemMessage(), true, next)
}

// renderForgotPasswordForm is GET /login/forgot: the page the sign-in form's
// "Forgot password?" link opens.
func (a *authHandlers) renderForgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	a.renderForgotPassword(w, r, http.StatusOK, "")
}

// finishBrowserForgotPassword confirms a reset request without revealing
// whether the address has an account (the JSON path is a constant 200).
func (a *authHandlers) finishBrowserForgotPassword(w http.ResponseWriter, r *http.Request, captured *capturedResponse) {
	if captured.status < http.StatusBadRequest {
		a.renderForgotPassword(w, r, http.StatusOK, resetRequestSent)
		return
	}
	a.renderForgotPassword(w, r, captured.status, captured.problemMessage())
}

func (a *authHandlers) renderForgotPassword(w http.ResponseWriter, r *http.Request, status int, flash string) {
	a.renderAuthPage(w, r, status, dashboard.Page{Title: "Reset your password", Body: "password_reset_request", Flash: flash})
}

func (a *authHandlers) renderAuthPage(w http.ResponseWriter, r *http.Request, status int, page dashboard.Page) {
	var buf bytesResponse
	buf.header = http.Header{}
	if err := dashboard.Render(&buf, a.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		a.log.Error("dashboard render auth page", "page", page.Body, "err", err)
		renderProblem(w, a.log, err)
		return
	}
	for key, values := range buf.header {
		w.Header()[key] = values
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf.body.Bytes())
}

// bytesResponse lets an auth page render before its status is chosen, so a
// template error still becomes a clean problem response.
type bytesResponse struct {
	header http.Header
	body   bytes.Buffer
}

func (b *bytesResponse) Header() http.Header         { return b.header }
func (b *bytesResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bytesResponse) WriteHeader(int)             {}
