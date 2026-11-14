package main

import (
	"errors"
	"net/http"
)

// livenessMaxRedirects matches net/http's default redirect budget.
const livenessMaxRedirects = 10

// livenessCheckRedirect follows a liveness probe's redirects only while they
// stay on the probed origin (scheme and host:port), so a trailing-slash or
// /login redirect still reaches the page behind it. A redirect anywhere else
// returns the 3xx itself (ADR-079 amendment 1): an app that sends /healthz
// to https://… (Spring requiresChannel, Django SECURE_SSL_REDIRECT, Rails
// force_ssl, Phoenix force_ssl) made the probe speak TLS to a plain-HTTP
// port, and an app that redirects to an outside login page sent the probe
// out of the VM. Both failed as transport errors and destroyed a healthy
// instance about 15 s after boot.
func livenessCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= livenessMaxRedirects {
		return errors.New("stopped after 10 redirects")
	}
	origin := via[0].URL
	if req.URL.Scheme != origin.Scheme || req.URL.Host != origin.Host {
		return http.ErrUseLastResponse
	}
	return nil
}
