package faas

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// GregaleClientReleaseOptions configures a client transport that learns and
// pins a project's release for public Gregale HTTP origins.
type GregaleClientReleaseOptions struct {
	// ManagedOrigins is the exact set of HTTP(S) origins that may receive a
	// release pin. Paths are ignored; schemes and hosts (including ports) must
	// match. At least one origin is required.
	ManagedOrigins []string
	// InitialRelease seeds the client with a release from SSR or application
	// bootstrap. When set, it must be a Gregale release UUID.
	InitialRelease string
}

// GregaleClientReleaseTransport learns a project release from the first
// response carrying a valid release header from a configured Gregale origin and pins subsequent
// requests from this transport instance to it. State is process-local and
// concurrency-safe. It returns expired-release responses (including 410)
// unchanged and never retries against the active release.
type GregaleClientReleaseTransport struct {
	base           http.RoundTripper
	managedOrigins map[string]struct{}

	mu         sync.Mutex
	release    string
	generation uint64
	discovery  *gregaleClientReleaseDiscovery
}

type gregaleClientReleaseDiscovery struct {
	done       chan struct{}
	generation uint64
}

// NewGregaleClientReleaseTransport creates a public-client transport. Only
// requests whose origin is listed in options.ManagedOrigins receive or teach
// this transport a release pin. Requests to other origins pass through
// unchanged. A nil base uses http.DefaultTransport.
func NewGregaleClientReleaseTransport(base http.RoundTripper, options GregaleClientReleaseOptions) (*GregaleClientReleaseTransport, error) {
	if len(options.ManagedOrigins) == 0 {
		return nil, fmt.Errorf("Gregale client release transport requires at least one managed origin")
	}

	managedOrigins := make(map[string]struct{}, len(options.ManagedOrigins))
	for _, value := range options.ManagedOrigins {
		origin, err := normalizeGregaleHTTPOrigin(value)
		if err != nil {
			return nil, fmt.Errorf("invalid Gregale managed origin %q: %w", value, err)
		}
		managedOrigins[origin] = struct{}{}
	}

	release := ""
	if options.InitialRelease != "" {
		var ok bool
		release, ok = cleanGregaleClientRelease(options.InitialRelease)
		if !ok {
			return nil, fmt.Errorf("initial Gregale release must be a UUID")
		}
	}
	if base == nil {
		base = http.DefaultTransport
	}

	return &GregaleClientReleaseTransport{
		base:           base,
		managedOrigins: managedOrigins,
		release:        release,
	}, nil
}

// Release returns this transport's currently captured release, if known.
func (t *GregaleClientReleaseTransport) Release() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.release, t.release != ""
}

// ClearRelease drops the captured pin. The next unpinned managed request can
// discover the active release again. In-flight discovery responses from the
// previous generation cannot restore the cleared pin.
func (t *GregaleClientReleaseTransport) ClearRelease() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.generation++
	t.release = ""
	if t.discovery != nil {
		discovery := t.discovery
		t.discovery = nil
		close(discovery.done)
	}
}

// RoundTrip implements http.RoundTripper.
func (t *GregaleClientReleaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, ok := gregaleRequestOrigin(req.URL)
	if !ok {
		return t.base.RoundTrip(req)
	}
	if _, managed := t.managedOrigins[origin]; !managed {
		return t.base.RoundTrip(req)
	}

	// Explicit pins are caller-owned. Preserve them (including a conflicting
	// pair, which the gateway will reject) and do not overwrite client state.
	explicitPin := hasGregaleHeader(req.Header, GregaleRevisionHeader) || hasGregaleHeader(req.Header, GregaleReleaseHeader)
	if explicitPin {
		return t.base.RoundTrip(req)
	}

	for {
		t.mu.Lock()
		if t.release != "" {
			release := t.release
			t.mu.Unlock()
			return t.base.RoundTrip(requestWithGregaleRelease(req, release))
		}

		if discovery := t.discovery; discovery != nil {
			t.mu.Unlock()
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-discovery.done:
			}

			t.mu.Lock()
			sameGeneration := t.generation == discovery.generation
			release := t.release
			t.mu.Unlock()
			if !sameGeneration {
				continue
			}
			if release != "" {
				continue // Loop once more to make a pinned request copy.
			}
			// This discovery completed without a usable release. Requests that
			// were already waiting proceed without a pin; a later call may retry
			// discovery, matching the browser adapter's fail-open bootstrap.
			return t.base.RoundTrip(req)
		}

		discovery := &gregaleClientReleaseDiscovery{
			done:       make(chan struct{}),
			generation: t.generation,
		}
		t.discovery = discovery
		t.mu.Unlock()

		response, err := t.base.RoundTrip(req)
		capturedRelease := ""
		if err == nil && response != nil {
			capturedRelease = gregaleReleaseFromResponse(response.Header)
		}

		t.mu.Lock()
		if t.discovery == discovery {
			if t.generation == discovery.generation && capturedRelease != "" {
				t.release = capturedRelease
			}
			t.discovery = nil
			close(discovery.done)
		}
		t.mu.Unlock()
		return response, err
	}
}

func requestWithGregaleRelease(req *http.Request, release string) *http.Request {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}
	cloned.Header.Set(GregaleReleaseHeader, release)
	return cloned
}

func hasGregaleHeader(headers http.Header, name string) bool {
	return len(headers.Values(name)) != 0
}

func gregaleReleaseFromResponse(headers http.Header) string {
	values := headers.Values(GregaleReleaseHeader)
	if len(values) != 1 {
		return ""
	}
	release, ok := cleanGregaleClientRelease(values[0])
	if !ok {
		return ""
	}
	return release
}

func cleanGregaleClientRelease(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return "", false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return "", false
			}
		}
	}
	return strings.ToLower(value), true
}

func gregaleRequestOrigin(requestURL *url.URL) (string, bool) {
	if requestURL == nil || requestURL.Host == "" {
		return "", false
	}
	origin, err := normalizeGregaleHTTPOrigin(strings.ToLower(requestURL.Scheme) + "://" + requestURL.Host)
	return origin, err == nil
}

func normalizeGregaleHTTPOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("must be an absolute HTTP or HTTPS origin without user information")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host, nil
}

var _ http.RoundTripper = (*GregaleClientReleaseTransport)(nil)
