// Package localpreview serves a validated immutable build on a loopback listener.
package localpreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

type content struct {
	file api.HostingFile
	body []byte
}
type Snapshot struct {
	files    map[string]content
	manifest api.HostingManifest
}

// New validates metadata and copies each body before any listener is created.
// read must supply exactly the bytes declared by the manifest, never execute code.
func New(manifest api.HostingManifest, read func(api.HostingFile) ([]byte, error)) (*Snapshot, error) {
	raw, err := json.Marshal(manifest)
	if err != nil || int64(len(raw)) > api.HostingManifestMaxBytes || hosting.ValidateManifest(manifest) != nil || read == nil {
		return nil, errors.New("invalid local preview manifest")
	}
	var copy api.HostingManifest
	if err = json.Unmarshal(raw, &copy); err != nil {
		return nil, err
	}
	out := &Snapshot{manifest: copy, files: make(map[string]content, len(copy.Files))}
	for _, file := range copy.Files {
		body, err := read(file)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		if int64(len(body)) != file.Size || hex.EncodeToString(sum[:]) != file.SHA256 {
			return nil, errors.New("build files changed during validation; restart with unchanged output")
		}
		out.files[file.Path] = content{file: file, body: append([]byte(nil), body...)}
	}
	return out, nil
}

// Handler accepts only the exact loopback authority selected by the listener.
// Access, DNS, TLS, old releases and CDN state are outside this local snapshot.
func (s *Snapshot) Handler(authority string) (http.Handler, error) {
	host, port, err := net.SplitHostPort(authority)
	n, e := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || e != nil || n < 1 || n > 65535 || port != strconv.Itoa(n) {
		return nil, errors.New("local preview requires an exact IPv4 loopback authority")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Gregale-Local-Preview", "true")
		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if r.Host != authority || err != nil || !net.ParseIP(remote).IsLoopback() || r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+authority {
			problem(w, r, 403, "local_preview_access_denied")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			problem(w, r, 405, "method_not_allowed")
			return
		}
		s.serve(w, r)
	}), nil
}
func (s *Snapshot) serve(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Path
	if !utf8.ValidString(requestPath) {
		problem(w, r, 503, "hosting_unavailable")
		return
	}
	path := strings.TrimPrefix(requestPath, "/")
	if path == "" || strings.HasSuffix(path, "/") {
		path += "index.html"
	}
	if !hosting.ValidPath(path) {
		problem(w, r, 404, "hosting_file_not_found")
		return
	}
	cfg := s.manifest.Config
	if serveRedirect(w, cfg, requestPath) {
		return
	}
	file, ok := s.files[path]
	if !ok {
		file, ok = s.files[path+"/index.html"]
	}
	html := strings.Contains(r.Header.Get("Accept"), "text/html")
	if !ok && s.manifest.SPAFallback && html && !strings.Contains(path[strings.LastIndex(path, "/")+1:], ".") {
		file, ok = s.files["index.html"]
	}
	status := 200
	if !ok && cfg != nil && cfg.NotFound != "" && html {
		file, ok = s.files[cfg.NotFound]
		status = 404
	}
	if !ok {
		problem(w, r, 404, "hosting_file_not_found")
		return
	}
	serveContent(w, r, file, cfg, requestPath, status)
}
func serveRedirect(w http.ResponseWriter, cfg *api.HostingReleaseConfig, path string) bool {
	if cfg == nil {
		return false
	}
	for _, redirect := range cfg.Redirects {
		if redirect.From == path {
			w.Header().Set("Location", redirect.To)
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			configuredHeaders(w.Header(), cfg, path)
			w.WriteHeader(redirect.Status)
			return true
		}
	}
	return false
}
func serveContent(w http.ResponseWriter, r *http.Request, c content, cfg *api.HostingReleaseConfig, path string, status int) {
	h := w.Header()
	etag := `"sha256:` + c.file.SHA256 + `"`
	h.Set("Content-Type", c.file.ContentType)
	h.Set("Content-Length", strconv.FormatInt(c.file.Size, 10))
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, max-age=0, must-revalidate")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	configuredHeaders(h, cfg, path)
	if status == 200 && r.Header.Get("If-None-Match") == etag {
		h.Del("Content-Length")
		w.WriteHeader(304)
		return
	}
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(c.body)
	}
}
func configuredHeaders(h http.Header, cfg *api.HostingReleaseConfig, path string) {
	if cfg == nil {
		return
	}
	for _, rule := range cfg.Headers {
		if rule.Path == path || strings.HasSuffix(rule.Path, "/*") && strings.HasPrefix(path, strings.TrimSuffix(rule.Path, "*")) {
			for key, value := range rule.Values {
				h.Set(key, value)
			}
		}
	}
}
func problem(w http.ResponseWriter, r *http.Request, status int, code string) {
	body, _ := json.Marshal(struct {
		Status int    `json:"status"`
		Code   string `json:"code"`
	}{status, code})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(body)
	}
}
