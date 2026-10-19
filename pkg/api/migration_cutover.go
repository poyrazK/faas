package api

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Recovery contains routing records only. Provider credentials and arbitrary
// notes are deliberately excluded from a saved cutover receipt.
type MigrationDNSRecord struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

type CreateMigrationCutoverRequest struct {
	ID                   string               `json:"id"`
	VerificationID       string               `json:"verification_id"`
	Hostname             string               `json:"hostname"`
	PreviousRecords      []MigrationDNSRecord `json:"previous_records"`
	PreviousProxyEnabled bool                 `json:"previous_proxy_enabled"`
	Max5xxRateBPS        int64                `json:"max_5xx_rate_bps"`
	MaxP95MS             int64                `json:"max_p95_ms"`
}

// ValidMigrationHostname accepts one concrete public DNS name, never a URL,
// wildcard, IP literal, port, single-label name or arbitrary record content.
func ValidMigrationHostname(host string) bool {
	if host != strings.ToLower(host) || len(host) > MigrationCutoverMaxHostnameBytes || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > MigrationCutoverMaxLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

func (r CreateMigrationCutoverRequest) Validate() error {
	for _, value := range []string{r.ID, r.VerificationID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ErrValidation("cutover and verification IDs must be canonical UUIDs")
		}
	}
	if !ValidMigrationHostname(r.Hostname) {
		return ErrValidation("hostname must be a concrete lowercase DNS name")
	}
	if len(r.PreviousRecords) == 0 || len(r.PreviousRecords) > MigrationCutoverMaxRecords {
		return ErrValidation("save the previous provider's routing records for recovery")
	}
	cnames := 0
	seen := map[string]bool{}
	for _, record := range r.PreviousRecords {
		if record.TTL < MigrationCutoverMinTTL || record.TTL > MigrationCutoverMaxTTL {
			return ErrValidation("previous DNS TTL must be between 0 and 2147483647 seconds")
		}
		valid := false
		switch record.Type {
		case "A", "AAAA":
			ip, err := netip.ParseAddr(record.Value)
			valid = err == nil && ip.Zone() == "" && ip.IsGlobalUnicast() && ((record.Type == "A" && ip.Is4()) || (record.Type == "AAAA" && ip.Is6() && !ip.Is4In6()))
		case "CNAME":
			target := strings.ToLower(strings.TrimSuffix(record.Value, "."))
			valid = ValidMigrationHostname(target) && target != r.Hostname
			cnames++
		}
		key := record.Type + ":" + record.Value
		if !valid || seen[key] {
			return ErrValidation("previous records must contain unique A, AAAA or CNAME routing values without URLs or credentials")
		}
		seen[key] = true
	}
	if cnames > 0 && len(r.PreviousRecords) != 1 {
		return ErrValidation("a CNAME cannot coexist with other routing records")
	}
	if r.Max5xxRateBPS < 0 || r.Max5xxRateBPS > RouteMonitorMaxRateBPS || r.MaxP95MS <= 0 || r.MaxP95MS > RouteHealthMaxP95BudgetMS {
		return ErrValidation("choose valid error and latency budgets")
	}
	return nil
}

type MigrationCutoverTraffic struct {
	Status           string     `json:"status"`
	Coverage         string     `json:"coverage"`
	Reason           string     `json:"reason"`
	Requests         int64      `json:"requests"`
	ServerErrors     int64      `json:"server_errors"`
	WorstWindowP95MS *float64   `json:"worst_window_p95_ms,omitempty"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
}

type MigrationCutover struct {
	ID                         string                        `json:"id"`
	App                        string                        `json:"app"`
	DeploymentID               string                        `json:"deployment_id"`
	ConfigurationRevision      string                        `json:"configuration_revision"`
	SmokePath                  string                        `json:"smoke_path"`
	Status                     string                        `json:"status"`
	Reason                     string                        `json:"reason,omitempty"`
	Request                    CreateMigrationCutoverRequest `json:"request"`
	DomainConnected            bool                          `json:"domain_connected"`
	DNSRecords                 []DNSRecordInstruction        `json:"dns_records"`
	Checks                     []MigrationVerificationCheck  `json:"checks"`
	Traffic                    MigrationCutoverTraffic       `json:"traffic"`
	CreatedAt                  time.Time                     `json:"created_at"`
	StartedAt                  *time.Time                    `json:"started_at,omitempty"`
	CheckDeadline              *time.Time                    `json:"check_deadline,omitempty"`
	CheckedAt                  *time.Time                    `json:"checked_at,omitempty"`
	HostnameVerifiedAt         *time.Time                    `json:"hostname_verified_at,omitempty"`
	CompletedAt                *time.Time                    `json:"completed_at,omitempty"`
	LimitedTrafficAcknowledged bool                          `json:"limited_traffic_acknowledged"`
}

type MigrationCutoverList struct {
	Plans []MigrationCutover `json:"plans"`
}
type MigrationCutoverActionRequest struct {
	Action                    string `json:"action"`
	AcknowledgeLimitedTraffic bool   `json:"acknowledge_limited_traffic"`
}

func migrationCutoverPath(app, id string) string {
	p := "/v1/apps/" + url.PathEscape(app) + "/migration-cutovers"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}
func (c *Client) CreateMigrationCutover(ctx context.Context, app string, in CreateMigrationCutoverRequest) (MigrationCutover, error) {
	var out MigrationCutover
	err := c.do(ctx, http.MethodPost, migrationCutoverPath(app, ""), in, &out)
	return out, err
}
func (c *Client) GetMigrationCutover(ctx context.Context, app, id string) (MigrationCutover, error) {
	var out MigrationCutover
	err := c.do(ctx, http.MethodGet, migrationCutoverPath(app, id), nil, &out)
	return out, err
}
func (c *Client) ListMigrationCutovers(ctx context.Context, app string) (MigrationCutoverList, error) {
	var out MigrationCutoverList
	err := c.do(ctx, http.MethodGet, migrationCutoverPath(app, ""), nil, &out)
	return out, err
}
func (c *Client) ActOnMigrationCutover(ctx context.Context, app, id string, in MigrationCutoverActionRequest) (MigrationCutover, error) {
	var out MigrationCutover
	err := c.do(ctx, http.MethodPost, migrationCutoverPath(app, id)+"/actions", in, &out)
	return out, err
}
