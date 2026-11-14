// Package disposableemail reports whether an email domain belongs to a
// throwaway inbox provider. domains.txt is a copy of the list the customer
// auth template ships (cmd/gregale/templates/auth/disposable-domains.txt,
// ADR-1190; CC0 1.0); a test keeps the two identical.
package disposableemail

import (
	_ "embed"
	"strings"
	"sync"
)

//go:embed domains.txt
var domainsFile string

var (
	loadOnce sync.Once
	domains  map[string]struct{}
)

func load() {
	domains = make(map[string]struct{}, 10_000)
	for _, line := range strings.Split(domainsFile, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domains[line] = struct{}{}
	}
}

// Domain reports whether domain, or a parent domain of it, is on the list.
// Matching is case-insensitive and ignores a trailing dot.
func Domain(domain string) bool {
	loadOnce.Do(load)
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	for d != "" {
		if _, ok := domains[d]; ok {
			return true
		}
		i := strings.IndexByte(d, '.')
		if i < 0 {
			return false
		}
		d = d[i+1:]
	}
	return false
}

// EmailDomain returns the lower-cased domain part of address, or "" when
// address has no "@".
func EmailDomain(address string) string {
	i := strings.LastIndexByte(address, '@')
	if i < 0 || i == len(address)-1 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(address[i+1:])), ".")
}
