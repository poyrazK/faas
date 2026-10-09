package geoip

// ADR-910: autonomous-system lookups for edge-rule match conditions. The
// DB-IP ASN Lite database is a separate MMDB file with the same licence,
// cadence and swap/reload mechanics as the country database, so it is
// opened as a second Reader and refreshed by a second Watcher.

import (
	"fmt"
	"log/slog"
	"net"
	"time"
)

// DBIPASNDownloadURL is the DB-IP ASN Lite monthly download.
const DBIPASNDownloadURL = "https://download.db-ip.com/free/dbip-asn-lite-%s.mmdb.gz"

type asnRecord struct {
	Number uint32 `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

// LookupASN returns the autonomous system an IP belongs to. Like Lookup it
// is fail-open: an IP outside the dataset returns (0, "", false, nil), and
// a nil Reader behaves as an empty database.
func (r *Reader) LookupASN(ip net.IP) (asn uint32, org string, ok bool, err error) {
	if r == nil || ip == nil {
		return 0, "", false, nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	r.curMu.RLock()
	cur := r.cur
	r.curMu.RUnlock()
	if cur == nil {
		return 0, "", false, nil
	}
	var rec asnRecord
	_, found, lerr := cur.LookupNetwork(ip, &rec)
	if lerr != nil {
		return 0, "", false, fmt.Errorf("geoip: asn lookup %v: %w", ip, lerr)
	}
	if !found || rec.Number == 0 {
		return 0, "", false, nil
	}
	return rec.Number, rec.Org, true, nil
}

// NewWatcherForURL is NewWatcher with a different monthly URL template
// (one %s for the YYYY-MM release), e.g. DBIPASNDownloadURL.
func NewWatcherForURL(r *Reader, interval time.Duration, urlTmpl string, log *slog.Logger) (*Watcher, error) {
	w, err := NewWatcher(r, interval, log)
	if err != nil {
		return nil, err
	}
	w.urlTmpl = urlTmpl
	return w, nil
}
