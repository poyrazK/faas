package main

// One-click browser login (ADR-20261111170000). The CLI listens on
// 127.0.0.1:<random port>, mints its code with that port and opens the
// approval page. The user clicks Authorize; the page sends a fresh device
// code to this listener and the CLI exchanges it. Nobody types a code. A
// terminal whose browser runs on another machine (SSH) uses the typed-code
// flow instead: `gregale login --device`, or automatically over SSH.

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// loopbackListen opens the one-click callback listener. A variable so tests
// can force the typed-code fallback.
var loopbackListen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

// remoteShell reports whether this terminal is an SSH session, where a
// browser opened by the user runs on another machine and cannot reach
// 127.0.0.1 here.
func remoteShell() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != ""
}

// browserLogin runs the interactive sign-in and saves the login. typed
// forces the typed-code flow.
func browserLogin(ctx context.Context, c *Client, typed bool) int {
	if !typed && !remoteShell() {
		if ln, err := loopbackListen(); err == nil {
			defer func() { _ = ln.Close() }()
			port := ln.Addr().(*net.TCPAddr).Port
			codeResp, err := c.MintCliAuthLoopbackCode(ctx, port)
			if err == nil {
				if codeResp.Loopback {
					return loopbackLogin(ctx, c, ln, codeResp)
				}
				// The server ignored the port: its code takes the typed flow.
				_ = ln.Close()
				return typedCodeLogin(ctx, c, codeResp)
			}
			// An older server refuses loopback_port (400): mint again
			// without it for the typed-code flow.
			var ae *APIError
			if !errors.As(err, &ae) || ae.Problem.Status != http.StatusBadRequest {
				return printErr("Could not start login", err)
			}
			_ = ln.Close()
		}
	}
	codeResp, err := c.MintCliAuthCode(ctx)
	if err != nil {
		return printErr("Could not start login", err)
	}
	return typedCodeLogin(ctx, c, codeResp)
}

// typedCodeLogin shows the code to type on the approval page and waits.
func typedCodeLogin(ctx context.Context, c *Client, codeResp api.CliAuthCodeResponse) int {
	_, _ = fmt.Fprintf(osStdout, "Your code: %s\n", codeResp.Code)
	_, _ = fmt.Fprintf(osStdout, "Open %s and enter the code there.\n", codeResp.URL)
	_, _ = fmt.Fprintln(osStdout, "  Never enter a code someone sent you.")
	if err := openBrowser(codeResp.URL); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		_, _ = fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", codeResp.URL)
	}
	_, _ = fmt.Fprintln(osStdout, "Waiting for approval...")
	return waitForApproval(ctx, c, codeResp)
}

// loopbackResult is what the callback hands back to loopbackLogin.
type loopbackResult struct {
	resp api.CliAuthExchangeResponse
	err  error
}

// loopbackLogin waits for the approving browser to bring the device code to
// ln, exchanges it and saves the login.
func loopbackLogin(ctx context.Context, c *Client, ln net.Listener, codeResp api.CliAuthCodeResponse) int {
	port := ln.Addr().(*net.TCPAddr).Port
	results := make(chan loopbackResult, 1)
	srv := &http.Server{
		Handler:           loopbackCallback(ctx, c, port, codeResp.Code, results),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", codeResp.URL)
	_, _ = fmt.Fprintln(osStdout, "  Click Authorize in your browser to sign in.")
	if err := openBrowser(codeResp.URL); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		_, _ = fmt.Fprintln(os.Stderr, "  Open the URL above in a browser on this computer, or run 'gregale login --device'.")
	}

	timeout := time.Until(loopbackDeadline(codeResp.ExpiresAt))
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return printErr("Login interrupted", ctx.Err())
	case <-timer.C:
		return printErr("Login expired", errors.New("the browser did not approve in time; run gregale login again"))
	case res := <-results:
		if res.err != nil {
			return printErr("Login failed", res.err)
		}
		return finalizeLogin(ctx, c, res.resp.Plaintext, res.resp.KeyID, res.resp.Account)
	}
}

// loopbackDeadline is when to stop waiting: the code's expiry plus a little
// slack, or the server's default lifetime when it sent none.
func loopbackDeadline(expiresAt string) time.Time {
	if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
		return t.Add(5 * time.Second)
	}
	return time.Now().Add(5 * time.Minute)
}

var loopbackPage = template.Must(template.New("loopback").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Gregale CLI</title></head>
<body style="font:15px/1.6 system-ui,sans-serif;max-width:26rem;margin:4rem auto;padding:0 1rem">
<p><strong>Gregale</strong></p>
<h1 style="font-size:1.25rem">{{.Title}}</h1>
<p>{{.Detail}}</p>
</body></html>`))

// loopbackCallback serves the one-click callback. It accepts only this
// login's own code on 127.0.0.1:port (a page elsewhere cannot rebind a name
// to it, and does not know the code), and only the first good callback.
func loopbackCallback(ctx context.Context, c *Client, port int, code string, results chan<- loopbackResult) http.Handler {
	var mu sync.Mutex
	done := false
	wantHost := "127.0.0.1:" + strconv.Itoa(port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodGet || r.URL.Path != api.CliAuthLoopbackCallbackPath || r.Host != wantHost {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if normalizeLoginCode(q.Get("code")) != normalizeLoginCode(code) || q.Get("device_code") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = loopbackPage.Execute(w, map[string]string{"Title": "Not this sign-in", "Detail": "This link is for a different terminal. Nothing was signed in."})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if done {
			_ = loopbackPage.Execute(w, map[string]string{"Title": "Already signed in", "Detail": "You can close this tab."})
			return
		}
		resp, err := c.ExchangeCliAuthLogin(ctx, api.CliAuthCodeResponse{Code: code, DeviceCode: q.Get("device_code")})
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = loopbackPage.Execute(w, map[string]string{"Title": "Sign-in failed", "Detail": "Return to your terminal for details."})
		} else {
			_ = loopbackPage.Execute(w, map[string]string{"Title": "You're signed in", "Detail": "The terminal is signed in as " + resp.Account.Email + ". You can close this tab."})
		}
		done = true
		results <- loopbackResult{resp: resp, err: err}
	})
}

// normalizeLoginCode compares codes as the server does: no dashes or
// surrounding space, upper case.
func normalizeLoginCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}
