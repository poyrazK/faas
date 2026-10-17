package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/livequery/liveproxy"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// liveRoutes is FAAS_REALTIME_LIVE_ROUTES_FILE: which database each live
// secret's key belongs to, and where that database's hub listens. Key IDs
// are not secret; the secrets stay with the hubs.
type liveRoutes struct {
	Routes []struct {
		KID      string `json:"kid"`
		Database string `json:"database"`
		URL      string `json:"url"`
	} `json:"routes"`
}

// liveProxy builds the live-query proxy from the routes file, or returns
// nil when live queries are off.
func liveProxy(path string, log *slog.Logger) (*liveproxy.Proxy, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-provisioned routes file named by the daemon's environment
	if err != nil {
		return nil, fmt.Errorf("realtimed: read live routes: %w", err)
	}
	var file liveRoutes
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("realtimed: parse live routes: %w", err)
	}
	routes := map[string]liveproxy.Route{}
	for _, r := range file.Routes {
		if len(r.KID) != 16 || r.Database == "" || (!strings.HasPrefix(r.URL, "ws://") && !strings.HasPrefix(r.URL, "wss://")) {
			return nil, fmt.Errorf("realtimed: live route %q: want a 16-character kid, a database and a ws:// or wss:// url", r.KID)
		}
		routes[r.KID] = liveproxy.Route{Database: r.Database, URL: r.URL}
	}
	log.Info("realtimed: live queries enabled", "routes", len(routes))
	return liveproxy.New(liveproxy.StaticRouter(routes), log), nil
}

// liveAdapter gives the proxy the realtime package's interface.
type liveAdapter struct{ proxy *liveproxy.Proxy }

func (a liveAdapter) Attach(send func([]byte) error) realtime.LiveSession {
	return a.proxy.Attach(send)
}
