package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/edgedns"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// newCertManager builds the certificate's manager from c: the ACME account
// key and the edge-tls recipient come from credential files, the DNS-01
// challenges go through dns into c's zone.
func newCertManager(c certConfig, dns *edgedns.Cloudflare, store edgecert.Store, metrics *edgecert.Metrics, log *slog.Logger) (*edgecert.Manager, error) {
	accountKey, err := os.ReadFile(c.accountKeyFile)
	if err != nil {
		return nil, fmt.Errorf("edgednsd: read the ACME account key: %w", err)
	}
	recipient, err := secretbox.LoadRecipient(c.recipientFile)
	if err != nil {
		return nil, fmt.Errorf("edgednsd: the edge-tls recipient: %w", err)
	}
	challenges, err := dns.DNS01(c.zoneID)
	if err != nil {
		return nil, fmt.Errorf("edgednsd: %w", err)
	}
	orderer, err := edgecert.NewLetsEncrypt(c.ca, accountKey, c.email, challenges, log.With("component", "acme"))
	clear(accountKey)
	if err != nil {
		return nil, fmt.Errorf("edgednsd: %w", err)
	}
	return &edgecert.Manager{
		Names:     c.names,
		Recipient: recipient,
		Orderer:   orderer,
		Store:     store,
		Metrics:   metrics,
		Log:       log,
	}, nil
}

// newTransparency builds the Certificate Transparency watch from c: it reads
// Cert Spotter, with the API key from its credential file when there is one,
// and tells edgednsd's certificates by the keys in store.
func newTransparency(c watchConfig, keys edgecert.KeyStore, metrics *edgecert.TransparencyMetrics, log *slog.Logger) (*edgecert.Transparency, error) {
	source := &edgecert.CertSpotter{Client: &http.Client{Timeout: api.EdgeCertificateTransparencyTimeout}}
	if c.apiKeyFile != "" {
		key, err := os.ReadFile(c.apiKeyFile)
		if err != nil {
			return nil, fmt.Errorf("edgednsd: read the Cert Spotter API key: %w", err)
		}
		source.Key = strings.TrimSpace(string(key))
	}
	return &edgecert.Transparency{
		Domain:  c.domain,
		Allow:   c.allow,
		Source:  source,
		Keys:    keys,
		Metrics: metrics,
		Log:     log,
	}, nil
}
