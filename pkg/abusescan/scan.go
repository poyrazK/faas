// Package abusescan finds known abuse tooling in a built image before it
// runs: cryptocurrency miners, mass port scanners, flood tools and proxy
// or tunnel servers (ADR-368).
//
// It is a signature scan, not an antivirus. The rules match distinctive
// strings that the real tools embed (option names, protocol methods, Go
// module paths), lowercased, so a renamed binary still matches. A packed
// or obfuscated binary evades it; the runtime egress controls (ADR-361)
// are the backstop. The value is catching the common case, an unmodified
// release binary or npm package, at no runtime cost.
//
// Every regular file up to MaxFileBytes is read, binaries and dependency
// trees included, because miners typically arrive as a vendored ELF binary
// or a package in node_modules. Media and archive formats are skipped by
// extension; their contents cannot be matched without unpacking.
package abusescan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Category groups rules for metrics and customer messages.
type Category string

const (
	CategoryMiner   Category = "miner"
	CategoryScanner Category = "scanner"
	CategoryFlood   Category = "flood"
	CategoryProxy   Category = "proxy"
)

// Action is what a match does to the deploy.
type Action string

const (
	// ActionBlock fails the deploy. Reserved for rules whose matches are
	// abuse tooling with no plausible use on the platform.
	ActionBlock Action = "block"
	// ActionFlag records an audit event and a metric for operator review.
	ActionFlag Action = "flag"
)

// Rule matches a file when every All pattern and at least MinAny of the Any
// patterns occur in it. Patterns are lowercase.
type Rule struct {
	ID       string
	Category Category
	Action   Action
	All      []string
	Any      []string
	MinAny   int
}

// Finding is one rule matching one file.
type Finding struct {
	Path     string   `json:"path"`
	RuleID   string   `json:"rule"`
	Category Category `json:"category"`
	Action   Action   `json:"action"`
}

// Rules is the built-in rule set.
var Rules = []Rule{
	{
		ID: "miner-xmrig", Category: CategoryMiner, Action: ActionBlock,
		All: []string{"xmrig"},
		Any: []string{"randomx", "cryptonight", "stratum+tcp", "stratum+ssl", "donate-level", "donate.v2.xmrig.com"}, MinAny: 1,
	},
	{
		// Stratum mining clients speak these JSON-RPC methods and URL schemes.
		ID: "miner-stratum", Category: CategoryMiner, Action: ActionBlock,
		Any:    []string{"stratum+tcp://", "stratum+ssl://", "stratum2+tcp://", "mining.subscribe", "mining.authorize", "mining.submit"},
		MinAny: 2,
	},
	{
		ID: "miner-pool-domain", Category: CategoryMiner, Action: ActionFlag,
		Any: []string{"supportxmr.com", "minexmr.com", "nanopool.org", "moneroocean.stream", "hashvault.pro",
			"c3pool.com", "herominers.com", "2miners.com", "unmineable.com", "nicehash.com", "f2pool.com",
			"xmrpool.eu", "ethermine.org", "viabtc.com"},
		MinAny: 1,
	},
	{
		ID: "scanner-masscan", Category: CategoryScanner, Action: ActionBlock,
		All: []string{"masscan"}, Any: []string{"--rate", "--banners", "--adapter-ip", "--source-port"}, MinAny: 2,
	},
	{
		ID: "scanner-zmap", Category: CategoryScanner, Action: ActionBlock,
		All: []string{"zmap"}, Any: []string{"--probe-module", "--target-port", "--bandwidth", "probe_modules"}, MinAny: 2,
	},
	{
		ID: "flood-hping3", Category: CategoryFlood, Action: ActionBlock,
		All: []string{"hping3"}, Any: []string{"--flood", "--rand-source"}, MinAny: 1,
	},
	{
		ID: "flood-mhddos", Category: CategoryFlood, Action: ActionBlock,
		All: []string{"mhddos"}, Any: []string{"layer7", "layer4", "amplification"}, MinAny: 1,
	},
	{
		// Go module paths embedded in proxy and tunnel server binaries.
		ID: "proxy-server", Category: CategoryProxy, Action: ActionFlag,
		Any: []string{"github.com/ginuerzh/gost", "github.com/go-gost/gost", "github.com/fatedier/frp",
			"github.com/jpillora/chisel", "github.com/xtls/xray-core", "github.com/v2fly/v2ray-core",
			"github.com/shadowsocks/go-shadowsocks2", "github.com/shadowsocks/shadowsocks-rust"},
		MinAny: 1,
	},
}

const (
	// MaxFileBytes caps the size of one scanned file. Larger files are
	// counted as skipped; miner and scanner binaries are a few MiB.
	MaxFileBytes = 64 << 20
	// MaxTotalBytes caps the bytes read per scanned tree so one huge image
	// cannot stall the deploy pipeline. Stats.Truncated reports hitting it.
	MaxTotalBytes = 2 << 30
	chunkBytes    = 4 << 20
)

// Stats summarises a tree scan.
type Stats struct {
	Files     int
	Bytes     int64
	Skipped   int
	Truncated bool
}

var skippedExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true, ".svg": true,
	".mp3": true, ".mp4": true, ".webm": true, ".ogg": true, ".wav": true, ".mov": true, ".avi": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true, ".pdf": true,
	".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".zst": true, ".7z": true, ".jar": true,
}

type compiled struct {
	patterns [][]byte
	maxLen   int
}

var ruleSet = compile(Rules)

func compile(rules []Rule) compiled {
	seen := map[string]bool{}
	var c compiled
	for _, r := range rules {
		for _, p := range append(append([]string(nil), r.All...), r.Any...) {
			if !seen[p] {
				seen[p] = true
				c.patterns = append(c.patterns, []byte(p))
				c.maxLen = max(c.maxLen, len(p))
			}
		}
	}
	return c
}

// ScanBytes matches the rules against one file's contents.
func ScanBytes(path string, data []byte) []Finding {
	return evaluate(path, matchChunk(bytes.ToLower(data), map[string]bool{}))
}

func matchChunk(lower []byte, found map[string]bool) map[string]bool {
	for _, p := range ruleSet.patterns {
		if !found[string(p)] && bytes.Contains(lower, p) {
			found[string(p)] = true
		}
	}
	return found
}

func evaluate(path string, found map[string]bool) []Finding {
	var out []Finding
	for _, r := range Rules {
		matched := true
		for _, p := range r.All {
			if !found[p] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		n := 0
		for _, p := range r.Any {
			if found[p] {
				n++
			}
		}
		if n < r.MinAny {
			continue
		}
		out = append(out, Finding{Path: path, RuleID: r.ID, Category: r.Category, Action: r.Action})
	}
	return out
}

// ScanTree walks dir and scans every regular file. Symlinks are not
// followed. Paths in findings are relative to dir with forward slashes.
func ScanTree(ctx context.Context, dir string) ([]Finding, Stats, error) {
	var findings []Finding
	var stats Stats
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if skippedExtensions[strings.ToLower(filepath.Ext(p))] {
			stats.Skipped++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %q: %w", p, err)
		}
		if info.Size() > MaxFileBytes || stats.Bytes+info.Size() > MaxTotalBytes {
			stats.Skipped++
			stats.Truncated = stats.Truncated || stats.Bytes+info.Size() > MaxTotalBytes
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		found, n, err := scanFile(p)
		if err != nil {
			return err
		}
		stats.Files++
		stats.Bytes += n
		findings = append(findings, evaluate(filepath.ToSlash(rel), found)...)
		return nil
	})
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].RuleID < findings[j].RuleID
	})
	return findings, stats, err
}

// scanFile reads p in chunks that overlap by the longest pattern, so a
// match across a chunk boundary is still seen.
func scanFile(p string) (map[string]bool, int64, error) {
	f, err := os.Open(p) //nolint:gosec,forbidigo // p comes from WalkDir over the staged image root and is a regular file.
	if err != nil {
		return nil, 0, fmt.Errorf("open %q: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	found := map[string]bool{}
	overlap := ruleSet.maxLen - 1
	buf := make([]byte, chunkBytes+overlap)
	carry := 0
	var total int64
	for {
		n, err := io.ReadFull(f, buf[carry:])
		total += int64(n)
		window := buf[:carry+n]
		matchChunk(bytes.ToLower(window), found)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return found, total, nil
		}
		if err != nil {
			return nil, total, fmt.Errorf("read %q: %w", p, err)
		}
		carry = min(overlap, len(window))
		copy(buf, window[len(window)-carry:])
	}
}

// Blocking reports whether any finding fails the deploy.
func Blocking(findings []Finding) bool {
	for _, f := range findings {
		if f.Action == ActionBlock {
			return true
		}
	}
	return false
}
