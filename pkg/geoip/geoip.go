// Package geoip wraps a MaxMind-compatible MMDB reader for the
// gateway's edge-rule geo gate (ADR-091 D21). The single public
// type is Reader; the gateway's applyEdgeRuleGeo calls Lookup
// against the trusted XFF-derived client IP (§4.1.2.8b).
//
// # Data source
//
// The reader is format-compatible with both MaxMind GeoLite2 and
// DB-IP Lite country databases. The team's deployment uses DB-IP
// Lite because it is CC-BY-4.0 licensed and does not require a
// MaxMind account key. The DB-IP file is downloaded at first boot
// from the URL listed in DBIPDownloadURL and refreshed in-process
// via the Watcher goroutine (see ENV FAAS_GEOIP_REFRESH_INTERVAL).
//
// # Attribution
//
// When the DB-IP file is loaded, the reader logs ONE line at INFO
// with the data source ("dbip") and the file age. The dashboard
// shows the equivalent attribution string in the footer, per
// CC-BY-4.0:
//
//	This product includes data created by DB-IP, available
//	from https://db-ip.com.
//
// # Failure mode
//
// Lookup is fail-open: a missing DB, an unreadable file, an IP
// outside the dataset's coverage (RFC1918, link-local, IPv6 ULA,
// Tor exits, etc.), or a decode error returns (country="", ok=false,
// err=nil-or-non-nil). The gateway's applyEdgeRuleGeo treats
// ok=false as "no country known → rule does not fire" — this is
// the §11 spirit (abuse gates must not break traffic). When err
// is non-nil, the gateway also emits a Warn log and increments
// the gateway_edge_rule_match_total{kind="geo",result="failed"}
// metric.
package geoip

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

// Source identifies the provenance of the MMDB file. Only the
// "dbip" flavour is wired today; the constant is here so a
// future MaxMind key (paid GeoIP2) can be added behind a
// config-time toggle without renaming the Reader API.
type Source string

const (
	SourceDBIP    Source = "dbip"
	SourceMaxMind Source = "maxmind"
)

// DBIPDownloadURL is the canonical DB-IP Lite download base
// URL. The .mmdb.gz file is downloaded once at boot (or on
// the watcher's tick) and atomically swapped into the live
// file path. The CC-BY-4.0 attribution is logged in the
// attribution string below.
const DBIPDownloadURL = "https://download.db-ip.com/free/dbip-country-lite-%s.mmdb.gz"

// DBIPAttribution is the CC-BY-4.0 attribution string that the
// dashboard + reader logs surface. The wording is dictated by
// the DB-IP license and must not be paraphrased.
const DBIPAttribution = "This product includes data created by DB-IP, available from https://db-ip.com."

// Reader is a thread-safe MMDB reader. The DB file is mmap'd by
// the underlying maxminddb.Reader; a Reload() opens a new
// instance and atomically swaps the pointer under curMu. Lookup
// reads through curMu.RLock() so a concurrent Reload never races
// against a Lookup on the same Reader.
//
// A zero-valued Reader is NOT usable — callers must construct via
// Open. The zero value is what the gateway uses on boot when the
// DB file is missing: pkg/gateway/handler.go's WithGeoReader
// setter accepts a nil Reader and applyEdgeRuleGeo no-ops on a
// nil reader (§11 fail-open).
type Reader struct {
	path    string
	source  Source
	attrib  string
	curMu   sync.RWMutex
	cur     *maxminddb.Reader
	openErr error        // last Open() error; preserved for the Watcher's first Reload
	bootAt  time.Time    // wall-clock at the most recent successful (re)load
	curPath atomic.Value // string, the path the current reader was opened from
	log     *slog.Logger
	// closeOnce guards Close from being called twice. The gateway
	// does not call Close today (the Reader lives for the lifetime
	// of the daemon), but tests do.
	closeOnce sync.Once
}

// mmdbRecord is the minimal shape every country-level MMDB answers.
// MaxMind GeoLite2-Country and DB-IP Lite-Country both populate
// `Country.ISOCode` with a 2-letter ISO 3166-1 alpha-2 code.
//
// The struct intentionally has only the fields we read. A future
// MaxMind GeoIP2 (paid) flavour exposes more (subdivision, city,
// etc.) — the Lookup helper only reads Country.ISOCode, so the
// additional fields are decoded into a sibling struct if/when
// that lands.
type mmdbRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// Open returns a Reader whose current DB is the file at `path`.
// The file is opened with maxminddb.Open which mmap's the file —
// calling Open on a missing file returns a wrapped error so the
// caller can decide whether to log-and-continue (the gateway's
// pattern) or fail boot (the test's pattern).
//
// `source` is the provenance label emitted in the boot log + the
// `geoip_db_source` Prometheus label. `attrib` is the human-readable
// attribution string surfaced in the dashboard footer.
func Open(path string, source Source, attrib string, log *slog.Logger) (*Reader, error) {
	if path == "" {
		return nil, errors.New("geoip: path is empty")
	}
	if log == nil {
		log = slog.Default()
	}
	r := &Reader{
		path:   path,
		source: source,
		attrib: attrib,
		bootAt: time.Now(),
		log:    log,
	}
	r.curPath.Store(path)
	if err := r.Reload(); err != nil {
		// Preserve the error so the Watcher can retry on its tick.
		r.openErr = err
		return r, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	r.openErr = nil
	return r, nil
}

// Reload re-opens the file at the Reader's path. The new
// maxminddb.Reader is atomic-loaded under curMu; concurrent
// Lookups either see the old reader or the new one, never a
// half-mmap'd mix. The Reader's bootAt is updated on success.
//
// A failed Reload returns the wrapped error and leaves the
// previous reader in place — the Watcher calls Reload
// periodically and a transient failure on a single tick must
// not blank the database.
func (r *Reader) Reload() error {
	r.curMu.Lock()
	defer r.curMu.Unlock()

	newReader, err := maxminddb.Open(r.path)
	if err != nil {
		return fmt.Errorf("geoip: reload %s: %w", r.path, err)
	}
	if r.cur != nil {
		// Best-effort close of the previous reader. The mmap
		// unmap is instant; errors from Close are not actionable
		// here (the file descriptor is already gone on the
		// reader we just replaced).
		_ = r.cur.Close()
	}
	r.cur = newReader
	r.bootAt = time.Now()
	r.curPath.Store(r.path)
	return nil
}

// Lookup returns the ISO 3166-1 alpha-2 country code for `ip`,
// along with a `ok` flag indicating whether the lookup found a
// record. The three return semantics are:
//
//	("US", true,  nil) — IP is in the US
//	("US", false, nil) — IP is in the US but the record had no iso_code (rare; DB-IP returns this for some reserved ranges)
//	("",   false, err) — DB lookup failed (DB missing, decode error, etc.); the gateway fail-opens
//	("",   false, nil) — IP is not in the dataset (RFC1918, link-local, IPv6 ULA, reserved, etc.)
//
// The err and ok flags are independent: a non-nil err signals a
// transient failure the operator should investigate (gauges
// tick), while a clean ("", false, nil) signals "no country known
// for this IP" — the gate fail-opens but nothing is wrong.
//
// nil-receiver safe: a nil Reader's Lookup returns ("", false, nil)
// so the gateway can wire WithGeoReader(nil) on boot when the
// DB file is missing.
func (r *Reader) Lookup(ip net.IP) (country string, ok bool, err error) {
	if r == nil || ip == nil {
		return "", false, nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	r.curMu.RLock()
	cur := r.cur
	r.curMu.RUnlock()
	if cur == nil {
		return "", false, nil
	}
	var rec mmdbRecord
	_, found, lerr := cur.LookupNetwork(ip, &rec)
	if lerr != nil {
		return "", false, fmt.Errorf("geoip: lookup %v: %w", ip, lerr)
	}
	if !found {
		return "", false, nil
	}
	if rec.Country.ISOCode == "" {
		return "", false, nil
	}
	return rec.Country.ISOCode, true, nil
}

// BootAt is the wall-clock time of the most recent successful
// (re)load. Staleness of the data itself is BuildTime.
// Returns the zero time if the reader has never been opened
// (the Watcher is still on its first attempt).
func (r *Reader) BootAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.curMu.RLock()
	defer r.curMu.RUnlock()
	return r.bootAt
}

// BuildTime is when the loaded database was built (its metadata
// build_epoch), or the zero time when none is loaded. DB-IP builds the
// country file monthly, so its age says how stale the country data is;
// BootAt only says when this process read the file.
func (r *Reader) BuildTime() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.curMu.RLock()
	defer r.curMu.RUnlock()
	if r.cur == nil {
		return time.Time{}
	}
	epoch := r.cur.Metadata.BuildEpoch
	if epoch == 0 || epoch > math.MaxInt64 {
		return time.Time{}
	}
	return time.Unix(int64(epoch), 0)
}

// Source is the provenance label of the loaded DB. Matches the
// `geoip_db_source` Prometheus label.
func (r *Reader) Source() Source {
	if r == nil {
		return ""
	}
	return r.source
}

// Attribution is the CC-BY-4.0 attribution string the dashboard
// footer prints. The string is configuration-supplied so a
// MaxMind-paid deployment can return its own attribution.
func (r *Reader) Attribution() string {
	if r == nil {
		return ""
	}
	return r.attrib
}

// Path is the on-disk file the Watcher reloads from. Returns
// "" for a nil Reader.
func (r *Reader) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Close releases the mmap'd region. The gateway does not call
// Close today (the Reader lives for the daemon's lifetime),
// but tests do. The closeOnce guards against the os.File
// double-close this would otherwise trigger.
func (r *Reader) Close() error {
	if r == nil {
		return nil
	}
	var closeErr error
	r.closeOnce.Do(func() {
		r.curMu.Lock()
		defer r.curMu.Unlock()
		if r.cur != nil {
			closeErr = r.cur.Close()
			r.cur = nil
		}
	})
	return closeErr
}

// SwappableDir returns the directory alongside `path` where the
// downloader writes its intermediate .tmp file. The tmp file is
// renamed over the live path on a successful decode so an
// in-flight Lookup never sees a half-written file.
//
// The sibling-directory choice mirrors the
// `state.Storage.TmpFileOf` pattern (storage-tmp-sibling-of-final):
// the tmp file is on the same filesystem as the live file so the
// rename is atomic, and the directory is the same one so an
// operator can stat it for fillness.
func SwappableDir(path string) string {
	return filepath.Dir(path)
}

// TmpPath is the .tmp sibling path the downloader writes to
// before the atomic rename. Returns the path with a `.tmp` suffix
// appended.
func TmpPath(path string) string {
	return path + ".tmp"
}

// MkdirAllForPath is the convenience helper a deployer calls
// once at boot to ensure the directory exists. It is a thin
// wrapper over os.MkdirAll(parent, 0o755) so the gatewayd
// daemon boot path can stay flat.
func MkdirAllForPath(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
