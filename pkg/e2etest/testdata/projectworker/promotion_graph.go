package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/e2etest/migrationprobe"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

func (w *workload) promotionCall(r *http.Request, target string, headers http.Header) (observation, int, error) {
	var report observation
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return report, 0, errors.New("invalid managed binding")
	}
	u.Path, u.RawQuery = "/promotion-protocol", ""
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if u.Scheme == "https" {
		pem, err := os.ReadFile(os.Getenv("GREGALE_SERVICE_CA_BUNDLE"))
		if err != nil {
			return report, 0, errors.New("managed trust unavailable")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return report, 0, errors.New("managed trust invalid")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return report, 0, err
	}
	// Only the fault-driver endpoint supplies attack headers. Ordinary calls
	// forward no release, deployment, revision or private probe credential.
	request.Header = headers.Clone()
	response, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		return report, 0, errors.New("managed call failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return report, response.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&report); err != nil {
		return report, response.StatusCode, err
	}
	return report, response.StatusCode, nil
}

func (w *workload) promotionDownstream(r *http.Request, report *observation) error {
	target := os.Getenv("GREGALE_SERVICE_BACKEND_URL")
	backend, status, err := w.promotionCall(r, target, nil)
	if err != nil || status != http.StatusOK {
		return errors.New("candidate dependency unavailable")
	}
	if backend.Generation != report.Generation || backend.Mode != report.Mode || backend.ReleaseID == "" || backend.DeploymentID == "" || backend.InstanceID == "" || !backend.DestinationSecretMatches || !report.DestinationSecretMatches {
		return errors.New("mixed promotion graph or destination credentials")
	}
	report.ReleaseID, report.BackendReleaseID = backend.ReleaseID, backend.ReleaseID
	report.BackendGeneration, report.BackendMode = backend.Generation, backend.Mode
	report.BackendDeploymentID, report.BackendInstanceID = backend.DeploymentID, backend.InstanceID
	report.BackendDestinationSecretMatches = backend.DestinationSecretMatches
	u, _ := url.Parse(target)
	report.BindingTransport = u.Scheme
	return nil
}

func (w *workload) servePromotionGraph(rw http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet && r.URL.Path == "/promotion-protocol" {
		w.mu.Lock()
		regressed := w.regressed
		w.mu.Unlock()
		if regressed {
			rw.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		report := w.snapshot()
		if config := migrationprobe.FromEnv(os.Getenv); config.Enabled() {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			err := config.Read(ctx, currentSQLGeneration(report.Generation))
			cancel()
			if err != nil {
				rw.WriteHeader(http.StatusServiceUnavailable)
				return true
			}
		}
		report.ReleaseID = r.Header.Get("X-Gregale-Release")
		writeObservation(rw, report)
		return true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/service-attack" && (os.Getenv("GRAPH_ROLE") == "frontend" || os.Getenv("GRAPH_ROLE") == "attack") {
		headers := http.Header{}
		for _, name := range []string{"X-Gregale-Release", "Gregale-Target-Deployment", "X-Gregale-Revision"} {
			if value := r.URL.Query().Get(name); value != "" {
				headers.Set(name, value)
			}
		}
		target := os.Getenv("GREGALE_SERVICE_BACKEND_URL")
		if value := r.URL.Query().Get("target"); value != "" {
			target = value
		}
		_, status, err := w.promotionCall(r, target, headers)
		if err != nil {
			rw.WriteHeader(http.StatusBadGateway)
		} else {
			rw.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(rw).Encode(map[string]int{"upstream_status": status}); err != nil {
				return true
			}
		}
		return true
	}
	return false
}
