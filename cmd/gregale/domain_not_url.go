package main

import (
	"fmt"
	"net/url"
	"strings"
)

// domainNotURL refuses a URL where a host name belongs and names the host:
// `domain add https://shop.example.com` answered `invalid character in DNS
// label "https://shop"` (prod hunt r64).
func domainNotURL(domain string) error {
	if !strings.Contains(domain, "://") && !strings.Contains(domain, "/") {
		return nil
	}
	raw := domain
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%q is not a host name; pass only the domain, e.g. shop.example.com", domain)
	}
	return fmt.Errorf("%q is a URL; pass only its host name: %s", domain, u.Hostname())
}
