package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/onebox-faas/faas/pkg/livequery/liveproxy"
)

// liveProxy builds the live-query proxy (ADR-20261016151947). Routes come with each
// endpoint's registration; the environment only configures how hubs are
// dialed. FAAS_REALTIME_LIVE_TLS_CA_FILE verifies wss:// hubs, and
// FAAS_REALTIME_LIVE_TLS_CERT_FILE with _KEY_FILE is realtimed's client
// certificate for them. FAAS_REALTIME_LIVE_TLS_SERVER_NAME verifies every
// hub as that name instead of its URL's host: the fleet PKI's hub leaf is
// livequeryd.faas wherever the hub runs.
func liveProxy(getenv func(string) string, log *slog.Logger) (*liveproxy.Proxy, error) {
	conf, err := liveTLSConfig(getenv)
	if err != nil {
		return nil, err
	}
	return liveproxy.New(log, conf), nil
}

// liveTLSConfig is how realtimed dials hubs over wss://, or nil for the
// system defaults.
func liveTLSConfig(getenv func(string) string) (*tls.Config, error) {
	caFile, certFile, keyFile := getenv("FAAS_REALTIME_LIVE_TLS_CA_FILE"), getenv("FAAS_REALTIME_LIVE_TLS_CERT_FILE"), getenv("FAAS_REALTIME_LIVE_TLS_KEY_FILE")
	serverName := getenv("FAAS_REALTIME_LIVE_TLS_SERVER_NAME")
	if (certFile == "") != (keyFile == "") {
		return nil, errors.New("realtimed: FAAS_REALTIME_LIVE_TLS_CERT_FILE and FAAS_REALTIME_LIVE_TLS_KEY_FILE go together")
	}
	if serverName != "" && caFile == "" {
		return nil, errors.New("realtimed: FAAS_REALTIME_LIVE_TLS_SERVER_NAME needs FAAS_REALTIME_LIVE_TLS_CA_FILE")
	}
	if caFile == "" && certFile == "" {
		return nil, nil
	}
	conf := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: serverName}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:forbidigo // operator-provisioned CA named by the daemon's environment
		if err != nil {
			return nil, fmt.Errorf("realtimed: read live hub CA: %w", err)
		}
		conf.RootCAs = x509.NewCertPool()
		if !conf.RootCAs.AppendCertsFromPEM(pem) {
			return nil, errors.New("realtimed: FAAS_REALTIME_LIVE_TLS_CA_FILE holds no certificate")
		}
	}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("realtimed: live hub client certificate: %w", err)
		}
		conf.Certificates = []tls.Certificate{cert}
	}
	return conf, nil
}
