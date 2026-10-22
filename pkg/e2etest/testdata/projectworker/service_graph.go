package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"
)

// This fixture forwards only its trusted ingress release to the managed binding.
// Incompatible wire generations make an accidental graph switch observable.
func (w *workload) downstream(r *http.Request, report *observation) error {
	if os.Getenv("PROMOTION_SERVICE_GRAPH") == "1" {
		if os.Getenv("GRAPH_ROLE") != "frontend" {
			return nil
		}
		return w.promotionDownstream(r, report)
	}
	target := os.Getenv("GREGALE_SERVICE_BACKEND_HTTPS_URL")
	if target == "" {
		return nil
	}
	release := r.Header.Get("X-Gregale-Release")
	if release == "" {
		return errors.New("missing ingress release")
	}
	pem, err := os.ReadFile(os.Getenv("GREGALE_SERVICE_CA_BUNDLE"))
	if err != nil {
		return errors.New("managed service trust unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("managed service trust invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target+"/protocol", nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Gregale-Release", release)
	request.Header.Set("X-Fixture-Protocol", report.Generation)
	response, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		return errors.New("managed service call failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("incompatible backend protocol")
	}
	var backend observation
	if err := json.NewDecoder(response.Body).Decode(&backend); err != nil {
		return err
	}
	if backend.Generation != report.Generation || backend.Mode != report.Mode || backend.ReleaseID != release {
		return errors.New("mixed managed release configuration")
	}
	report.ReleaseID, report.BackendReleaseID = release, backend.ReleaseID
	report.BackendGeneration, report.BackendMode = backend.Generation, backend.Mode
	return nil
}
