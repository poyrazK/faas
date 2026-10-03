// adr: 368 — build-time abuse signature scan.
package abusescan

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanBytesRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		data   string
		rule   string
		action Action
	}{
		{"xmrig release binary", "\x7fELF...XMRig 6.21.0 built with RandomX support --donate-level", "miner-xmrig", ActionBlock},
		{"stratum client", `{"method":"mining.subscribe"} ... pool: stratum+tcp://pool.example:3333`, "miner-stratum", ActionBlock},
		{"pool domain only", "const pool = 'gulf.moneroocean.stream:10128'", "miner-pool-domain", ActionFlag},
		{"masscan", "masscan 1.3.2 usage: --rate <pps> --banners", "scanner-masscan", ActionBlock},
		{"zmap", "zmap --probe-module=tcp_synscan --target-port=22", "scanner-zmap", ActionBlock},
		{"hping3", "hping3 -S --flood --rand-source", "flood-hping3", ActionBlock},
		{"proxy server", "go.buildid ... github.com/fatedier/frp/server", "proxy-server", ActionFlag},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanBytes("f", []byte(tc.data))
			found := false
			for _, f := range got {
				if f.RuleID == tc.rule {
					found = true
					if f.Action != tc.action {
						t.Fatalf("%s action = %s, want %s", tc.rule, f.Action, tc.action)
					}
				}
			}
			if !found {
				t.Fatalf("findings %+v, want rule %s", got, tc.rule)
			}
		})
	}
}

// Words that appear in ordinary code and docs must not trip a blocking rule
// on their own.
func TestScanBytesBenign(t *testing.T) {
	for _, data := range []string{
		"# Security\nWe scan for XMRig and other miners in CI.",
		"Our stratum dashboard shows your hashrate. Protocol: stratum+tcp://",
		"masscan is a port scanner; we do not ship it.",
		"import { flood } from './fill'; hping3 docs",
	} {
		if got := ScanBytes("README.md", []byte(data)); Blocking(got) {
			t.Fatalf("benign text %q blocked: %+v", data, got)
		}
	}
}

func TestScanTree(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, data []byte) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A match straddling the first chunk boundary.
	boundary := append(bytes.Repeat([]byte("a"), chunkBytes-5), []byte("stratum+tcp://h mining.submit")...)
	write("node_modules/evil/bin/run", boundary)
	write("app/server.js", []byte("console.log('hello')"))
	write("assets/logo.png", []byte("xmrig randomx")) // skipped extension
	big := filepath.Join(dir, "huge.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	findings, stats, err := ScanTree(context.Background(), dir)
	if err != nil {
		t.Fatalf("ScanTree: %v", err)
	}
	if len(findings) != 1 || findings[0].Path != "node_modules/evil/bin/run" || findings[0].RuleID != "miner-stratum" {
		t.Fatalf("findings = %+v, want one miner-stratum in node_modules/evil/bin/run", findings)
	}
	if !Blocking(findings) {
		t.Fatal("a miner-stratum finding must block")
	}
	if stats.Files != 2 || stats.Skipped != 2 || stats.Truncated {
		t.Fatalf("stats = %+v, want 2 scanned, 2 skipped (png, oversize), not truncated", stats)
	}
}
