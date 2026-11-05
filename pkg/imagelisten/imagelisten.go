// Package imagelisten tells the repository scan whether a prebuilt Compose
// image listens: whether its config's ExposedPorts names a TCP port
// (ADR-20261029120000 amendment 2). apid and githubd use the same lookup, so
// a plan, its apply and a later push deploy classify a service alike.
//
// Only anonymous pulls are tried, through the egress-guarded registry client:
// registry credentials stay with imaged, which unseals them at deploy time.
// A private image, a registry that is down or a timeout is "unknown", which
// the scan reports and reconcile treats conservatively.
package imagelisten

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

const (
	// DefaultTimeout bounds one registry lookup. The plan waits on it, so an
	// unreachable registry costs at most this per image.
	DefaultTimeout = 5 * time.Second
	// DefaultTTL is how long a definite answer is reused. A tag can move, so
	// the answer is not kept for good.
	DefaultTTL = 10 * time.Minute
	// maxEntries bounds the cache; past it the cache starts over.
	maxEntries = 1024
)

// ConfigPuller fetches an image's config without its layers.
type ConfigPuller interface {
	PullImageConfig(ctx context.Context, ref string) (oci.ImageConfig, error)
}

// Lookup answers reposcan.ImageListening from a registry.
type Lookup struct {
	puller  ConfigPuller
	timeout time.Duration
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]answer
}

type answer struct {
	listens bool
	at      time.Time
}

// New returns a Lookup over puller.
func New(puller ConfigPuller, timeout, ttl time.Duration) *Lookup {
	return &Lookup{puller: puller, timeout: timeout, ttl: ttl, now: time.Now, cache: map[string]answer{}}
}

// NewRegistry returns a Lookup over an anonymous registry client whose dials
// refuse loopback, private and metadata addresses.
func NewRegistry() *Lookup {
	client := oci.NewRegistryClient(oci.WithHTTPClient(oci.NewEgressHTTPClient()), oci.WithTimeout(DefaultTimeout))
	return New(client, DefaultTimeout, DefaultTTL)
}

// Listening reports whether image declares a TCP port. known is false when
// the registry could not say; that answer is not cached, so the next scan
// asks again.
func (l *Lookup) Listening(image string) (listens, known bool) {
	now := l.now()
	l.mu.Lock()
	cached, ok := l.cache[image]
	l.mu.Unlock()
	if ok && now.Sub(cached.at) < l.ttl {
		return cached.listens, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	cfg, err := l.puller.PullImageConfig(ctx, image)
	if err != nil {
		return false, false
	}
	listens = exposesTCP(cfg.ExposedPorts)
	l.mu.Lock()
	if len(l.cache) >= maxEntries {
		l.cache = map[string]answer{}
	}
	l.cache[image] = answer{listens: listens, at: now}
	l.mu.Unlock()
	return listens, true
}

// exposesTCP reports whether an ExposedPorts set names a TCP port: "8080/tcp",
// or "8080", which OCI reads as TCP.
func exposesTCP(exposed map[string]struct{}) bool {
	for spec := range exposed {
		port, protocol, _ := strings.Cut(spec, "/")
		if port != "" && (protocol == "" || strings.EqualFold(protocol, "tcp")) {
			return true
		}
	}
	return false
}
