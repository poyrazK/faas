package objectstorage

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// PublicReadForwardConfig configures the public-read matcher of an edge that
// holds no storage credentials (ADR-1343 §2). It needs only the read-only
// lookup state; Forward serves every request a public bucket would answer.
type PublicReadForwardConfig struct {
	Store      PublicReadStore
	AppsDomain string
	Forward    http.Handler
	Next       http.Handler
	Log        *slog.Logger
}

type publicReadForwarder struct {
	match   *publicReadHandler
	forward http.Handler
	next    http.Handler
	log     *slog.Logger
}

// NewPublicReadForwarder matches requests exactly as NewPublicReadHandler
// does and hands the matched ones to Forward instead of serving them, so a
// compute-node edge sends public object reads to the control plane without
// provider credentials. Misses go to Next untouched.
func NewPublicReadForwarder(c PublicReadForwardConfig) (http.Handler, error) {
	if c.Store == nil || c.Forward == nil || c.Next == nil {
		return nil, errors.New("object storage public read forwarder: store, forward and next are required")
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
	appsDomain := strings.Trim(strings.ToLower(c.AppsDomain), ".")
	if appsDomain == "" {
		appsDomain = "gregale.dev"
	}
	return &publicReadForwarder{
		match:   &publicReadHandler{store: c.Store, appsDomain: appsDomain},
		forward: c.Forward, next: c.Next, log: c.Log,
	}, nil
}

func (f *publicReadForwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		f.next.ServeHTTP(w, r)
		return
	}
	_, _, matched, err := f.match.lookup(r)
	if err != nil && !matched {
		f.log.Warn("public object lookup failed", "host", r.Host, "path", r.URL.Path)
		f.next.ServeHTTP(w, r)
		return
	}
	if matched {
		f.forward.ServeHTTP(w, r)
		return
	}
	f.next.ServeHTTP(w, r)
}

// IsUploadPath reports whether path belongs to the upload handler: an upload
// route (POST /uploads/{route}) or one of its receipts
// (/uploads/{route}/receipts/{id}). A compute-node edge forwards these to the
// control plane, whose upload handler passes any route an app does not
// declare on to the app as usual.
func IsUploadPath(path string) bool {
	routePath, _, isReceipt := strings.Cut(path, "/receipts/")
	if isReceipt {
		_, ok := uploadRouteName(routePath)
		return ok
	}
	_, ok := uploadRouteName(path)
	return ok
}
