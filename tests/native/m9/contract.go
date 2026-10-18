// Package m9 defines the first native two-node migration qualification.
// spec: §14.A
// adr: 1529
// adr: 1530
package m9

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

type Peer struct {
	Name       string `json:"name"`
	SSH        string `json:"ssh"`
	Socket     string `json:"socket"`
	VMMDSHA256 string `json:"vmmd_sha256"`
}

// Config identifies an operator-provisioned, scale-to-zero fixture deployment.
// DATABASE_URL remains in the environment and never enters an evidence report.
type Config struct {
	Version        int          `json:"version"`
	AppID          string       `json:"app_id"`
	DeploymentID   string       `json:"deployment_id"`
	FixtureID      string       `json:"fixture_id"`
	TimeoutSeconds int          `json:"timeout_seconds"`
	Source         Peer         `json:"source"`
	Destination    Peer         `json:"destination"`
	Drain          *DrainConfig `json:"drain,omitempty"`
}

// DrainConfig opts into the API/controller drill. Credentials stay in the
// environment; the same reviewed schedd binary must run on both peers.
type DrainConfig struct {
	APIURL       string `json:"api_url"`
	ScheddSHA256 string `json:"schedd_sha256"`
}

func (c DrainConfig) Validate() error {
	u, err := url.Parse(c.APIURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		strings.ContainsAny(c.APIURL, "?#") || u.Path != "" && u.Path != "/" {
		return errors.New("M9 drain API must be an HTTPS origin without credentials, query or fragment")
	}
	digest, err := hex.DecodeString(c.ScheddSHA256)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != c.ScheddSHA256 {
		return errors.New("M9 drain requires the reviewed schedd SHA-256")
	}
	return nil
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,253}$`)
var safeSocket = regexp.MustCompile(`^/run/faas/[a-zA-Z0-9_/.-]+\.sock$`)

func DecodeConfig(r io.Reader) (Config, error) {
	var c Config
	data, err := io.ReadAll(io.LimitReader(r, 65537))
	if err != nil || len(data) > 65536 {
		return c, errors.New("M9 configuration is unreadable or exceeds 64 KiB")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("M9 configuration: %w", err)
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return c, errors.New("M9 configuration has trailing data")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 180
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Version != 1 || !CanonicalUUID(c.AppID) || !CanonicalUUID(c.DeploymentID) || !CanonicalUUID(c.FixtureID) {
		return errors.New("M9 requires version 1 and canonical fixture UUIDs")
	}
	if c.TimeoutSeconds < 60 || c.TimeoutSeconds > 300 {
		return errors.New("M9 timeout must be between 60 and 300 seconds")
	}
	for _, p := range []Peer{c.Source, c.Destination} {
		digest, err := hex.DecodeString(p.VMMDSHA256)
		if !safeName.MatchString(p.Name) || !safeName.MatchString(p.SSH) ||
			!safeSocket.MatchString(p.Socket) || path.Clean(p.Socket) != p.Socket ||
			err != nil || len(digest) != 32 || hex.EncodeToString(digest) != p.VMMDSHA256 {
			return errors.New("M9 peer requires a safe name, SSH target, /run/faas socket and lowercase VMMD SHA-256")
		}
	}
	if c.Source.Name == c.Destination.Name || c.Source.SSH == c.Destination.SSH {
		return errors.New("M9 requires distinct source and destination peers")
	}
	if c.Source.VMMDSHA256 != c.Destination.VMMDSHA256 {
		return errors.New("M9 requires the same reviewed vmmd artifact on both peers")
	}
	if c.Drain != nil {
		return c.Drain.Validate()
	}
	return nil
}

func CanonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
