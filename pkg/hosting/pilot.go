package hosting

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// pilotTransport is explicitly opted in for disposable qualification apids only.
// The private relay performs normal verified HTTPS to the fixed Cloudflare API.
// R2, KV, Worker, and unrelated apid transports never use this relay.
type pilotTransport struct {
	socket string
	local  *http.Transport
}

func privatePilotSocket(path string) error {
	if !filepath.IsAbs(path) || len(path) > api.HostingPilotRelaySocketMaxBytes || strings.ContainsAny(path, "\r\n") {
		return errors.New("pilot relay requires a short absolute private Unix socket")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("pilot relay socket must exist without symlinks")
	}
	for _, item := range []struct {
		path   string
		socket bool
	}{{filepath.Dir(path), false}, {path, true}} {
		info, err := os.Lstat(item.path)
		if err != nil {
			return errors.New("pilot relay private filesystem boundary unavailable")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || (item.socket && info.Mode()&os.ModeSocket == 0) || (!item.socket && !info.IsDir()) {
			return errors.New("pilot relay directory and socket must be private and owned by apid uid")
		}
	}
	return nil
}

func newPilotTransport(path string) (*pilotTransport, error) {
	if err := privatePilotSocket(path); err != nil {
		return nil, err
	}
	t := &pilotTransport{socket: path}
	// No proxy or TCP fallback on this local hop. The relay preserves the remote
	// proxy and CA configuration on its outbound HTTPS connection.
	t.local = &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := privatePilotSocket(path); err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	return t, nil
}
func (t *pilotTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.URL.User != nil || !strings.HasPrefix(r.URL.Path, "/client/v4/") {
		return nil, errors.New("pilot relay only accepts the fixed Cloudflare management API")
	}
	if err := privatePilotSocket(t.socket); err != nil {
		return nil, err
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = "http", "hosting-pilot.local"
	clone.URL, clone.Host = &u, "hosting-pilot.local"
	return t.local.RoundTrip(clone)
}

func configurePilot(cfg *Config, getenv func(string) string) error {
	enabled := getenv("FAAS_FRONTEND_HOSTING_PILOT_ENABLED")
	socket := getenv("FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET")
	if enabled != "1" && enabled != "true" {
		if socket != "" {
			return errors.New("pilot relay socket requires explicit pilot opt-in")
		}
		return nil
	}
	if cfg.Domains == nil && cfg.Analytics == nil {
		return errors.New("pilot relay requires a hosting management or analytics client")
	}
	transport, err := newPilotTransport(socket)
	if err != nil {
		return err
	}
	if cfg.Domains != nil {
		cfg.Domains.client.Transport = transport
	}
	if cfg.Analytics != nil {
		cfg.Analytics.client.Transport = transport
	}
	return nil
}
