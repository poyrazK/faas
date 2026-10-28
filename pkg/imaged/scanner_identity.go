package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// scannerIdentity names the scanner and vulnerability database a scan used,
// in the same fields a scan report records (ADR-20261028102540). Grype's
// findings are a function of the scanned bytes, the Grype version and the
// database build, so a scan of unchanged bytes under an unchanged identity can
// only repeat the stored one.
type scannerIdentity struct {
	Version   string
	DBVersion string
	DBBuiltAt string
}

// newScannerIdentity normalizes report metadata. ok is false when any part is
// missing or the build time is unparsable, and such an identity never matches.
func newScannerIdentity(version, dbVersion, dbBuiltAt string) (scannerIdentity, bool) {
	version, dbVersion = strings.TrimSpace(version), strings.TrimSpace(dbVersion)
	built, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(dbBuiltAt))
	if version == "" || dbVersion == "" || err != nil {
		return scannerIdentity{}, false
	}
	return scannerIdentity{Version: version, DBVersion: dbVersion, DBBuiltAt: built.UTC().Format(time.RFC3339Nano)}, true
}

// matches reports whether a stored scan's metadata names this identity.
func (id scannerIdentity) matches(version, dbVersion, dbBuiltAt string) bool {
	other, ok := newScannerIdentity(version, dbVersion, dbBuiltAt)
	return ok && id.Version != "" && other == id
}

// scannerIdentityCache keeps the last identity read from the probe or seen in
// a real scan's result.
type scannerIdentityCache struct {
	mu   sync.Mutex
	id   scannerIdentity
	seen time.Time
}

func (c *scannerIdentityCache) get(now time.Time) (scannerIdentity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id.Version == "" || now.Sub(c.seen) >= api.ScannerIdentityCacheTTL || now.Before(c.seen) {
		return scannerIdentity{}, false
	}
	return c.id, true
}

func (c *scannerIdentityCache) set(id scannerIdentity, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id, c.seen = id, now
}

// observe records the identity a real scan reported; unparsable metadata
// clears the cache so the next decision asks the probe.
func (c *scannerIdentityCache) observe(version, dbVersion, dbBuiltAt string, now time.Time) {
	id, ok := newScannerIdentity(version, dbVersion, dbBuiltAt)
	if !ok {
		c.set(scannerIdentity{}, time.Time{})
		return
	}
	c.set(id, now)
}

// WithScannerIdentity installs the probe that reads the installed scanner and
// database. Without one, nothing is reused and every renewal scans.
func (h *Handler) WithScannerIdentity(probe func(context.Context) (scannerIdentity, error)) *Handler {
	h.scannerIdentityProbe = probe
	return h
}

var errScannerIdentityUnavailable = errors.New("imaged: scanner identity probe is not configured")

// currentScannerIdentity returns the installed scanner identity. An error means
// "unknown", and callers then scan instead of reusing.
func (h *Handler) currentScannerIdentity(ctx context.Context) (scannerIdentity, error) {
	if h.scannerIdentityProbe == nil {
		return scannerIdentity{}, errScannerIdentityUnavailable
	}
	now := time.Now()
	if id, ok := h.scannerIdentityCache.get(now); ok {
		return id, nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.ScannerIdentityProbeTimeout)
	defer cancel()
	id, err := h.scannerIdentityProbe(ctx)
	if err != nil {
		return scannerIdentity{}, err
	}
	if id.Version == "" {
		return scannerIdentity{}, errors.New("imaged: scanner identity probe returned no identity")
	}
	h.scannerIdentityCache.set(id, now)
	return id, nil
}

// WithGrypeIdentityProbe reads the scanner identity from the Grype binary the
// scans use (FAAS_GRYPE_BIN; empty means grype on PATH).
func (h *Handler) WithGrypeIdentityProbe(bin string) *Handler {
	h.scannerDBUpdater = grypeDBUpdater(bin)
	return h.WithScannerIdentity(grypeIdentityProbe(bin))
}

// WithScannerDBUpdater installs the database refresh; nil disables it.
func (h *Handler) WithScannerDBUpdater(update func(context.Context) error) *Handler {
	h.scannerDBUpdater = update
	return h
}

// grypeDBUpdater runs `grype db update`, which downloads a newer database
// when one is published and otherwise only checks.
func grypeDBUpdater(bin string) func(context.Context) error {
	if bin == "" {
		bin = "grype"
	}
	return func(ctx context.Context) error {
		_, _, err := runBoundedScanCommand(ctx, api.ScannerIdentityMaxOutputBytes, bin, "db", "update")
		return err
	}
}

// refreshScannerDB updates the vulnerability database and forgets the cached
// scanner identity, so the next reuse decision compares against the database
// now installed. Without it, reused scans would leave Grype's own update
// check, which runs only inside a scan, nearly idle.
func (h *Handler) refreshScannerDB(ctx context.Context) {
	if h.scannerDBUpdater == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, api.ScannerDBUpdateTimeout)
	defer cancel()
	err := h.scannerDBUpdater(ctx)
	h.scannerIdentityCache.set(scannerIdentity{}, time.Time{})
	if err != nil && h.log != nil {
		h.log.Warn("imaged: vulnerability database update failed", "err", err)
	}
}

// grypeIdentityProbe reads the Grype version and the installed vulnerability
// database with `grype version -o json` and `grype db status -o json`. Neither
// command scans or updates the database.
func grypeIdentityProbe(bin string) func(context.Context) (scannerIdentity, error) {
	if bin == "" {
		bin = "grype"
	}
	return func(ctx context.Context) (scannerIdentity, error) {
		versionOut, _, err := runBoundedScanCommand(ctx, api.ScannerIdentityMaxOutputBytes, bin, "version", "-o", "json")
		if err != nil {
			return scannerIdentity{}, err
		}
		statusOut, _, err := runBoundedScanCommand(ctx, api.ScannerIdentityMaxOutputBytes, bin, "db", "status", "-o", "json")
		if err != nil {
			return scannerIdentity{}, err
		}
		return parseGrypeIdentity(versionOut, statusOut)
	}
}

// parseGrypeIdentity reads `grype version -o json` and `grype db status -o
// json`. The database must report itself valid.
func parseGrypeIdentity(versionOut, statusOut []byte) (scannerIdentity, error) {
	var version struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(versionOut, &version); err != nil {
		return scannerIdentity{}, fmt.Errorf("imaged: decode grype version: %w", err)
	}
	var status struct {
		SchemaVersion string `json:"schemaVersion"`
		Built         string `json:"built"`
		Valid         *bool  `json:"valid"`
	}
	if err := json.Unmarshal(statusOut, &status); err != nil {
		return scannerIdentity{}, fmt.Errorf("imaged: decode grype db status: %w", err)
	}
	if status.Valid == nil || !*status.Valid {
		return scannerIdentity{}, errors.New("imaged: grype reports its vulnerability database invalid")
	}
	id, ok := newScannerIdentity(version.Version, status.SchemaVersion, status.Built)
	if !ok {
		return scannerIdentity{}, errors.New("imaged: grype identity is incomplete")
	}
	return id, nil
}
