package gateway

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// DNSBlocklist refuses tenant lookups of abuse infrastructure at the pinned
// bridge resolver (ADR-370). Every guest's DNS is DNAT'd to this resolver
// (ADR-361), so a blocked name cannot be resolved another way; with
// DNS-gated egress it cannot be reached either.
//
// Matching is by domain suffix on label boundaries: "nanopool.org" blocks
// "xmr-eu1.nanopool.org" but not "notnanopool.org".
type DNSBlocklist struct {
	byDomain map[string]string
}

// builtinDNSBlocklist is mining pool infrastructure. Operators add threat
// feeds (malware, C2, phishing) through FAAS_DNS_BLOCKLIST_FILE.
var builtinDNSBlocklist = map[string]string{
	"supportxmr.com": "miner", "minexmr.com": "miner", "nanopool.org": "miner",
	"moneroocean.stream": "miner", "hashvault.pro": "miner", "c3pool.com": "miner",
	"herominers.com": "miner", "2miners.com": "miner", "unmineable.com": "miner",
	"nicehash.com": "miner", "f2pool.com": "miner", "xmrpool.eu": "miner",
	"ethermine.org": "miner", "viabtc.com": "miner", "minergate.com": "miner",
	"prohashing.com": "miner", "zpool.ca": "miner", "xmrig.com": "miner",
}

// NewDNSBlocklist returns the built-in list plus extra (domain → category).
func NewDNSBlocklist(extra map[string]string) *DNSBlocklist {
	b := &DNSBlocklist{byDomain: make(map[string]string, len(builtinDNSBlocklist)+len(extra))}
	for d, c := range builtinDNSBlocklist {
		b.byDomain[d] = c
	}
	for d, c := range extra {
		if d = normalizeBlockedDomain(d); d != "" {
			b.byDomain[d] = c
		}
	}
	return b
}

// Match reports whether name or any parent domain is blocked, and its
// category.
func (b *DNSBlocklist) Match(name string) (string, bool) {
	if b == nil {
		return "", false
	}
	name = normalizeBlockedDomain(name)
	for name != "" {
		if category, ok := b.byDomain[name]; ok {
			return category, true
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return "", false
		}
		name = name[i+1:]
	}
	return "", false
}

// Len is the number of blocked domains.
func (b *DNSBlocklist) Len() int {
	if b == nil {
		return 0
	}
	return len(b.byDomain)
}

func normalizeBlockedDomain(d string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
}

// LoadDNSBlocklistFile reads an operator blocklist: one domain per line,
// optionally followed by a category (default "operator"). Blank lines and
// lines starting with # are ignored.
func LoadDNSBlocklistFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec,forbidigo // operator-configured blocklist path from the daemon's environment.
	if err != nil {
		return nil, fmt.Errorf("dns blocklist: %w", err)
	}
	defer func() { _ = f.Close() }()
	return parseDNSBlocklist(f)
}

func parseDNSBlocklist(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		domain := normalizeBlockedDomain(fields[0])
		if domain == "" || !strings.Contains(domain, ".") {
			return nil, fmt.Errorf("dns blocklist line %d: %q is not a domain", n, fields[0])
		}
		category := "operator"
		if len(fields) > 1 {
			category = strings.ToLower(fields[1])
		}
		out[domain] = category
	}
	return out, sc.Err()
}
