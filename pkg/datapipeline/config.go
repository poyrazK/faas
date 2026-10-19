package datapipeline

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// Config is operator-owned TOML. An explicit account allowlist and exact source
// destinations are mandatory during qualification. No wildcard admission.
type Config struct {
	Enabled       bool                               `toml:"enabled"`
	Accounts      []string                           `toml:"accounts"`
	Hosts         []string                           `toml:"hosts"`
	CIDRs         []string                           `toml:"cidrs"`
	RecipientPath string                             `toml:"recipient_path"`
	IdentityPath  string                             `toml:"identity_path"`
	Capacity      []connectioncapacity.Qualification `toml:"capacity"`
}

func (c Config) Allowed(account string) bool {
	return c.Enabled && slices.Contains(c.Accounts, account)
}
func (c Config) Policy() (commit.NetworkPolicy, error) {
	p := commit.NetworkPolicy{Hosts: map[string]bool{}}
	if err := validateQualifications(c.Capacity, time.Now()); err != nil {
		return p, err
	}
	for _, host := range c.Hosts {
		if host == "" || strings.ContainsAny(host, "/\\:@* \r\n") {
			return p, ErrInvalid
		}
		p.Hosts[host] = true
	}
	for _, cidr := range c.CIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return p, ErrInvalid
		}
		p.Prefixes = append(p.Prefixes, prefix.Masked())
	}
	for _, account := range c.Accounts {
		if !validID(account) {
			return p, ErrInvalid
		}
	}
	if c.Enabled && (len(c.Accounts) == 0 || len(c.Accounts) > api.DataPipelinePilotAccountsMax || len(p.Hosts) == 0 || len(p.Prefixes) == 0 || c.RecipientPath == "" || c.IdentityPath == "") {
		return p, ErrInvalid
	}
	return p, nil
}

// LoadIdentities keeps the previous key usable during an operator-managed
// rotation. The API's public sealing key must match the worker's current key.
func (c Config) LoadIdentities() ([]*age.X25519Identity, error) {
	if !c.Enabled {
		return nil, ErrUnavailable
	}
	current, err := secretbox.LoadHostKey(c.IdentityPath)
	if err != nil {
		return nil, ErrUnavailable
	}
	recipient, err := secretbox.LoadRecipient(c.RecipientPath)
	if err != nil || recipient.String() != current.Recipient().String() {
		return nil, ErrInvalid
	}
	keys := []*age.X25519Identity{current}
	previous, err := secretbox.LoadHostKey(c.IdentityPath + ".previous")
	if err == nil {
		keys = append(keys, previous)
	} else if !errors.Is(err, secretbox.ErrHostKeyNotFound) {
		return nil, ErrInvalid
	}
	return keys, nil
}
