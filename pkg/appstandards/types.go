// Package appstandards resolves company application requirements without
// database or daemon dependencies. All consumers use the same field semantics.
package appstandards

import (
	"encoding/json"
	"time"
)

type Field string

const (
	LogDestinations   Field = "log_destinations"
	RequireSigned     Field = "require_signed"
	SecurityPolicy    Field = "security_policy"
	TrustedPublishers Field = "trusted_publishers"
	EgressCIDRs       Field = "egress_cidrs"
	EgressExtraPorts  Field = "egress_extra_ports"
)

type Mode string

const (
	Default    Mode = "default"
	Mandatory  Mode = "mandatory"
	Restricted Mode = "restricted"
)

type Override string

const (
	NoOverride Override = "none"
	Narrow     Override = "narrow"
	Extend     Override = "extend"
)

type Rule struct {
	Mode     Mode            `json:"mode"`
	Value    json.RawMessage `json:"value"`
	Override Override        `json:"override,omitempty"`
}

type Definition map[Field]Rule
type Settings map[Field]json.RawMessage

// Limits are supplied from pkg/api/limits.go at the public boundary.
type Limits struct {
	DefinitionBytes int
	SetEntries      int
	Layers          int
}

type Layer struct {
	StandardID string     `json:"standard_id"`
	Version    int64      `json:"version"`
	Scope      string     `json:"scope"`
	Definition Definition `json:"definition"`
}

// Exceptions replace one requirement in one immutable standard version.
// Authorization, resource ownership and maximum lifetime are checked by apid.
type Exception struct {
	ID         string          `json:"id"`
	StandardID string          `json:"standard_id"`
	Version    int64           `json:"version"`
	Field      Field           `json:"field"`
	Value      json.RawMessage `json:"value"`
	Reason     string          `json:"reason"`
	ExpiresAt  time.Time       `json:"expires_at"`
}

type Source struct {
	StandardID  string   `json:"standard_id"`
	Version     int64    `json:"version"`
	Scope       string   `json:"scope"`
	Mode        Mode     `json:"mode"`
	Override    Override `json:"override"`
	ExceptionID string   `json:"exception_id,omitempty"`
}

type Violation struct {
	Field  Field  `json:"field"`
	Code   string `json:"code"`
	Source Source `json:"source"`
}

type Effective struct {
	Values     Settings           `json:"values"`
	Sources    map[Field][]Source `json:"sources"`
	Violations []Violation        `json:"violations"`
}
