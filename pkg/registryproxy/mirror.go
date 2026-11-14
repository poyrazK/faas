package registryproxy

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Mirror is the registry mirror a builder VM's BuildKit pulls one private
// registry through (ADR-20261110210000 slice 3j-3). vmmd serves it to the
// guest, which then holds no registry password: every request goes to
// Transport (ForBuild), so secretd adds the credential.
//
// It answers the registry API's version check itself, accepts only GET and
// HEAD of /v2/ paths, rewrites them to https://Registry/v2/..., and drops
// the "ns" query parameter BuildKit adds when it calls a mirror. The guest's
// request headers do not reach secretd beyond those Transport forwards.
type Mirror struct {
	Registry  string
	Transport http.RoundTripper
}

// ServeHTTP implements http.Handler.
func (m *Mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "the build registry mirror only pulls", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/v2" || r.URL.Path == "/v2/" {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "{}")
		}
		return
	}
	upstream, ok := m.upstream(r.URL)
	if !ok {
		http.Error(w, "the build registry mirror serves only /v2/ paths", http.StatusNotFound)
		return
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, upstream, nil)
	if err != nil {
		http.Error(w, "bad registry path", http.StatusBadRequest)
		return
	}
	for _, name := range forwardHeaders {
		for _, v := range r.Header.Values(name) {
			out.Header.Add(name, v)
		}
	}
	resp, err := m.Transport.RoundTrip(out)
	if err != nil {
		// BuildKit falls back to the registry itself, which refuses an
		// anonymous pull of a private image: the build log says so.
		http.Error(w, "the control plane did not pull from "+m.Registry, http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for name, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil && !errors.Is(err, io.EOF) {
		// The guest sees a short body and retries; nothing else to do.
		return
	}
}

// upstream maps a mirror request to the registry URL secretd fetches.
func (m *Mirror) upstream(in *url.URL) (string, bool) {
	if !strings.HasPrefix(in.Path, "/v2/") || strings.Contains(in.Path, "/../") || strings.HasSuffix(in.Path, "/..") {
		return "", false
	}
	q := in.Query()
	q.Del("ns")
	u := url.URL{Scheme: "https", Host: m.Registry, Path: in.Path, RawQuery: q.Encode()}
	return u.String(), true
}
