package agentproxy

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Blocklist is the complaint-fed list of domains no agent reaches
// (ADR-20261117185346 §4). An entry blocks that name and every name below
// it. Load replaces the list whole.
type Blocklist struct {
	mu      sync.RWMutex
	domains map[string]bool
}

// Load reads one domain per line; blank lines and # comments are skipped.
func (b *Blocklist) Load(r io.Reader) error {
	next := map[string]bool{}
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text, _, _ := strings.Cut(sc.Text(), "#")
		text = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(text)), ".")
		if text == "" {
			continue
		}
		if !validHostname(text) {
			return fmt.Errorf("blocklist line %d: %q is not a domain", line, text)
		}
		next[text] = true
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read blocklist: %w", err)
	}
	b.mu.Lock()
	b.domains = next
	b.mu.Unlock()
	return nil
}

// Blocks reports whether host or a domain above it is listed.
func (b *Blocklist) Blocks(host string) bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for name := host; name != ""; {
		if b.domains[name] {
			return true
		}
		_, rest, ok := strings.Cut(name, ".")
		if !ok {
			break
		}
		name = rest
	}
	return false
}

// Len is the number of listed domains.
func (b *Blocklist) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.domains)
}
