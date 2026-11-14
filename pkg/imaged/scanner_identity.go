package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// installedScanner names the scanner and vulnerability database a scan used,
// in the same fields a scan report records (ADR-20261028102540). Grype's
// findings are a function of the scanned bytes, the Grype version and the
// database build, so a scan of unchanged bytes under an unchanged identity can
// only repeat the stored one.
type installedScanner struct {
	Version   string
	DBVersion string
	DBBuiltAt string
}

// newScannerIdentity normalizes report metadata. ok is false when any part is
// missing or the build time is unparsable, and such an identity never matches.
func newScannerIdentity(version, dbVersion, dbBuiltAt string) (installedScanner, bool) {
	version, dbVersion = strings.TrimSpace(version), strings.TrimSpace(dbVersion)
	built, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(dbBuiltAt))
	if version == "" || dbVersion == "" || err != nil {
		return installedScanner{}, false
	}
	return installedScanner{Version: version, DBVersion: dbVersion, DBBuiltAt: built.UTC().Format(time.RFC3339Nano)}, true
}

// matches reports whether a stored scan's metadata names this identity.
func (id installedScanner) matches(version, dbVersion, dbBuiltAt string) bool {
	other, ok := newScannerIdentity(version, dbVersion, dbBuiltAt)
	return ok && id.Version != "" && other == id
}

// scannerIdentityCache keeps the last identity read from the probe or seen in
// a real scan's result. stamp is the install stamp read just before the probe
// that produced id; empty when unknown.
type scannerIdentityCache struct {
	mu    sync.Mutex
	id    installedScanner
	seen  time.Time
	stamp string
}

// get returns the cached identity while it is younger than
// api.ScannerIdentityCacheTTL, or, when stamp is known and equals the stamp
// the identity was probed under, while it is younger than
// api.ScannerIdentityUnchangedMaxAge: the binary and database files the probe
// read are the ones still installed.
func (c *scannerIdentityCache) get(now time.Time, stamp string) (installedScanner, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id.Version == "" || now.Before(c.seen) {
		return installedScanner{}, false
	}
	age := now.Sub(c.seen)
	if age < api.ScannerIdentityCacheTTL {
		return c.id, true
	}
	if stamp != "" && stamp == c.stamp && age < api.ScannerIdentityUnchangedMaxAge {
		return c.id, true
	}
	return installedScanner{}, false
}

func (c *scannerIdentityCache) set(id installedScanner, now time.Time) {
	c.setProbed(id, now, "")
}

func (c *scannerIdentityCache) setProbed(id installedScanner, now time.Time, stamp string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id, c.seen, c.stamp = id, now, stamp
}

// observe records the identity a real scan reported; unparsable metadata
// clears the cache so the next decision asks the probe. A scan that reports
// the identity already cached keeps its install stamp; any other identity
// drops it, since the scan may have updated the database.
func (c *scannerIdentityCache) observe(version, dbVersion, dbBuiltAt string, now time.Time) {
	id, ok := newScannerIdentity(version, dbVersion, dbBuiltAt)
	if !ok {
		c.set(installedScanner{}, time.Time{})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id != c.id {
		c.stamp = ""
	}
	c.id, c.seen = id, now
}

// WithScannerIdentity installs the probe that reads the installed scanner and
// database. Without one, nothing is reused and every renewal scans.
func (h *Handler) WithScannerIdentity(probe func(context.Context) (installedScanner, error)) *Handler {
	h.scannerIdentityProbe = probe
	return h
}

var errScannerIdentityUnavailable = errors.New("imaged: scanner identity probe is not configured")

// currentScannerIdentity returns the installed scanner identity. An error means
// "unknown", and callers then scan instead of reusing.
func (h *Handler) currentScannerIdentity(ctx context.Context) (installedScanner, error) {
	if h.scannerIdentityProbe == nil {
		return installedScanner{}, errScannerIdentityUnavailable
	}
	now := time.Now()
	// Read the stamp before probing: a change during the probe then leaves a
	// stamp that no longer matches, and the next call probes again.
	stamp := ""
	if h.scannerInstallStamp != nil {
		stamp = h.scannerInstallStamp()
	}
	if id, ok := h.scannerIdentityCache.get(now, stamp); ok {
		return id, nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.ScannerIdentityProbeTimeout)
	defer cancel()
	id, err := h.scannerIdentityProbe(ctx)
	if err != nil {
		return installedScanner{}, err
	}
	if id.Version == "" {
		return installedScanner{}, errors.New("imaged: scanner identity probe returned no identity")
	}
	h.scannerIdentityCache.setProbed(id, now, stamp)
	return id, nil
}

// WithScannerInstallStamp installs the cheap check that the scanner and its
// database are unchanged since the last probe. Nil, or an empty stamp, keeps
// the identity only for api.ScannerIdentityCacheTTL.
func (h *Handler) WithScannerInstallStamp(stamp func() string) *Handler {
	h.scannerInstallStamp = stamp
	return h
}

// WithGrypeIdentityProbe reads the scanner identity from the Grype binary the
// scans use (FAAS_GRYPE_BIN; empty means grype on PATH). dbDir is Grype's
// database directory (GRYPE_DB_CACHE_DIR); when set, the identity is kept
// while neither the binary nor any file there has changed.
func (h *Handler) WithGrypeIdentityProbe(bin, dbDir string) *Handler {
	h.scannerDBUpdater = grypeDBUpdater(bin)
	if dbDir != "" {
		h.WithScannerInstallStamp(grypeInstallStamp(bin, dbDir))
	}
	return h.WithScannerIdentity(grypeIdentityProbe(bin))
}

// grypeInstallStamp describes the Grype binary and every file under its
// database directory by device, inode, size and modification time. Grype
// installs a new database by writing new files, so any update changes the
// stamp. Any error, or a directory past api.ScannerInstallStampMaxEntries
// entries, gives the empty stamp, which never matches.
func grypeInstallStamp(bin, dbDir string) func() string {
	if bin == "" {
		bin = "grype"
	}
	return func() string {
		path, err := exec.LookPath(bin)
		if err != nil {
			return ""
		}
		var b strings.Builder
		if !appendFileStamp(&b, "bin", path) {
			return ""
		}
		entries := 0
		err = filepath.WalkDir(dbDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entries++; entries > api.ScannerInstallStampMaxEntries {
				return errScannerStampTooLarge
			}
			rel, err := filepath.Rel(dbDir, p)
			if err != nil {
				return err
			}
			if !appendFileStamp(&b, rel, p) {
				return errScannerStampUnreadable
			}
			return nil
		})
		if err != nil {
			return ""
		}
		return b.String()
	}
}

var (
	errScannerStampTooLarge   = errors.New("imaged: grype database directory has too many entries to stamp")
	errScannerStampUnreadable = errors.New("imaged: grype install file could not be stamped")
)

// appendFileStamp writes one file's identity: name, device, inode, size,
// mode and modification time.
func appendFileStamp(b *strings.Builder, name, path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	fmt.Fprintf(b, "%s|%d|%d|%d|%o|%d\n", name, st.Dev, st.Ino, info.Size(), info.Mode(), info.ModTime().UnixNano())
	return true
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
	h.scannerIdentityCache.set(installedScanner{}, time.Time{})
	if err != nil && h.log != nil {
		h.log.Warn("imaged: vulnerability database update failed", "err", err)
	}
}

// grypeIdentityProbe reads the Grype version and the installed vulnerability
// database with `grype version -o json` and `grype db status -o json`. Neither
// command scans or updates the database.
func grypeIdentityProbe(bin string) func(context.Context) (installedScanner, error) {
	if bin == "" {
		bin = "grype"
	}
	return func(ctx context.Context) (installedScanner, error) {
		versionOut, _, err := runBoundedScanCommand(ctx, api.ScannerIdentityMaxOutputBytes, bin, "version", "-o", "json")
		if err != nil {
			return installedScanner{}, err
		}
		statusOut, _, err := runBoundedScanCommand(ctx, api.ScannerIdentityMaxOutputBytes, bin, "db", "status", "-o", "json")
		if err != nil {
			return installedScanner{}, err
		}
		return parseGrypeIdentity(versionOut, statusOut)
	}
}

// parseGrypeIdentity reads `grype version -o json` and `grype db status -o
// json`. The database must report itself valid.
func parseGrypeIdentity(versionOut, statusOut []byte) (installedScanner, error) {
	var version struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(versionOut, &version); err != nil {
		return installedScanner{}, fmt.Errorf("imaged: decode grype version: %w", err)
	}
	var status struct {
		SchemaVersion string `json:"schemaVersion"`
		Built         string `json:"built"`
		Valid         *bool  `json:"valid"`
	}
	if err := json.Unmarshal(statusOut, &status); err != nil {
		return installedScanner{}, fmt.Errorf("imaged: decode grype db status: %w", err)
	}
	if status.Valid == nil || !*status.Valid {
		return installedScanner{}, errors.New("imaged: grype reports its vulnerability database invalid")
	}
	id, ok := newScannerIdentity(version.Version, status.SchemaVersion, status.Built)
	if !ok {
		return installedScanner{}, errors.New("imaged: grype identity is incomplete")
	}
	return id, nil
}
