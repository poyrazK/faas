package hosting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrDomainProviderConflict = errors.New("hosting domain provider ownership conflict")
var ErrDomainProviderUnavailable = errors.New("hosting domain provider unavailable")
var errDomainProviderMissing = errors.New("hosting domain provider object missing")

type DomainProvider interface {
	ID() string
	Target() string
	Ensure(context.Context, api.HostingDomain, bool) (api.HostingDomainDetails, bool, error)
	Remove(context.Context, api.HostingDomain) error
}

type CloudflareDomains struct {
	account, zone, policy, token, target, id string
	base                                     string
	client                                   *http.Client
}

func NewCloudflareDomains(account, zone, policy, token, target string) (*CloudflareDomains, error) {
	normalized, err := NormalizeDomain(target, nil)
	if !hexID(account) || !hexID(zone) || uuid.Validate(policy) != nil || token == "" || err != nil || normalized != target {
		return nil, errors.New("hosting domains require an isolated SaaS zone, internal Access policy, scoped token, and canonical CNAME target")
	}
	digest := sha256.Sum256([]byte(account + "\n" + zone + "\n" + policy + "\n" + target))
	return &CloudflareDomains{account: account, zone: zone, policy: policy, token: token, target: target, id: hex.EncodeToString(digest[:]), base: "https://api.cloudflare.com/client/v4", client: &http.Client{Timeout: api.HostingDomainProviderTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (p *CloudflareDomains) ID() string       { return p.id }
func (p *CloudflareDomains) Target() string   { return p.target }
func (p *CloudflareDomains) hostPath() string { return "/zones/" + p.zone + "/custom_hostnames" }
func (p *CloudflareDomains) appPath() string  { return "/accounts/" + p.account + "/access/apps" }
func (p *CloudflareDomains) appName(d api.HostingDomain) string {
	return "gregale-internal-hosting-" + d.ID + "-" + p.id[:12]
}

func (p *CloudflareDomains) validateZone(ctx context.Context, fallback bool) error {
	var zone struct {
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if _, err := p.request(ctx, "GET", "/zones/"+p.zone, nil, &zone); err != nil {
		return err
	}
	if zone.Name == "" || zone.Account.ID != p.account || !strings.HasSuffix(p.target, "."+zone.Name) {
		return ErrDomainProviderConflict
	}
	if fallback {
		var origin struct {
			Origin string `json:"origin"`
			Status string `json:"status"`
		}
		if _, err := p.request(ctx, "GET", p.hostPath()+"/fallback_origin", nil, &origin); err != nil {
			return err
		}
		if origin.Origin != p.target || origin.Status != "active" {
			return ErrDomainProviderUnavailable
		}
	}
	return nil
}

type cfDomainEnvelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Info    struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}
type cfDomainHostname struct {
	OriginServer string               `json:"custom_origin_server"`
	OriginSNI    string               `json:"custom_origin_sni"`
	ID           string               `json:"id"`
	Hostname     string               `json:"hostname"`
	Status       string               `json:"status"`
	Metadata     map[string]string    `json:"custom_metadata"`
	Ownership    api.HostingDNSRecord `json:"ownership_verification"`
	SSL          struct {
		Status     string `json:"status"`
		Method     string `json:"method"`
		Type       string `json:"type"`
		Validation []struct {
			TXTName     string `json:"txt_name"`
			TXTValue    string `json:"txt_value"`
			CNAME       string `json:"cname"`
			CNAMETarget string `json:"cname_target"`
		} `json:"validation_records"`
	} `json:"ssl"`
}
type cfDomainApp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Type     string `json:"type"`
	AUD      string `json:"aud"`
	Policies []struct {
		ID string `json:"id"`
	} `json:"policies"`
}

func (p *CloudflareDomains) request(ctx context.Context, method, path string, body any, out any) (int, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return 0, ErrDomainProviderUnavailable
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, ErrDomainProviderUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return 0, ErrDomainProviderUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return 0, errDomainProviderMissing
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return 0, ErrDomainProviderUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, api.HostingDomainProviderMaxBytes+1))
	var envelope cfDomainEnvelope
	if err != nil || len(data) > api.HostingDomainProviderMaxBytes || json.Unmarshal(data, &envelope) != nil || !envelope.Success {
		return 0, ErrDomainProviderUnavailable
	}
	if out != nil && json.Unmarshal(envelope.Result, out) != nil {
		return 0, ErrDomainProviderUnavailable
	}
	return envelope.Info.TotalPages, nil
}

func (p *CloudflareDomains) validHost(h cfDomainHostname, d api.HostingDomain) bool {
	if uuid.Validate(h.ID) != nil || h.Hostname != d.Hostname || h.OriginServer != "" || h.OriginSNI != "" || h.SSL.Method != "txt" || h.SSL.Type != "dv" {
		return false
	}
	// Preserve ownership checks on legacy Enterprise objects, but never require metadata.
	return len(h.Metadata) == 0 || h.Metadata["gregale_domain_id"] == d.ID && h.Metadata["gregale_origin_id"] == p.ID()
}

func (p *CloudflareDomains) findHost(ctx context.Context, d api.HostingDomain) (*cfDomainHostname, error) {
	if d.Details.ProviderID == "" {
		return nil, nil
	}
	var host cfDomainHostname
	_, err := p.request(ctx, "GET", p.hostPath()+"/"+d.Details.ProviderID, nil, &host)
	if errors.Is(err, errDomainProviderMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if host.ID != d.Details.ProviderID || !p.validHost(host, d) {
		return nil, ErrDomainProviderConflict
	}
	return &host, nil
}

// ValidateRecovery reads the exact operator-reviewed resource; it never discovers
// or adopts resources through a hostname search and never creates anything.
func (p *CloudflareDomains) ValidateRecovery(ctx context.Context, d api.HostingDomain, id string) error {
	if uuid.Validate(id) != nil {
		return ErrDomainProviderConflict
	}
	if err := p.validateZone(ctx, false); err != nil {
		return err
	}
	d.Details.ProviderID = id
	host, err := p.findHost(ctx, d)
	if err != nil {
		return err
	}
	if host == nil {
		return ErrDomainProviderConflict
	}
	return nil
}

func (p *CloudflareDomains) ownedApp(app cfDomainApp, d api.HostingDomain) bool {
	return uuid.Validate(app.ID) == nil && app.Name == p.appName(d) && app.Domain == d.Hostname && app.Type == "self_hosted"
}
func (p *CloudflareDomains) findApps(ctx context.Context, d api.HostingDomain) ([]cfDomainApp, error) {
	var apps []cfDomainApp
	for page := 1; page <= api.HostingDomainProviderPagesMax; page++ {
		var items []cfDomainApp
		pages, err := p.request(ctx, "GET", p.appPath()+"?name="+url.QueryEscape(p.appName(d))+"&domain="+url.QueryEscape(d.Hostname)+"&per_page=50&page="+strconv.Itoa(page), nil, &items)
		if err != nil {
			return nil, err
		}
		for _, app := range items {
			if !p.ownedApp(app, d) {
				return nil, ErrDomainProviderConflict
			}
			apps = append(apps, app)
		}
		if pages <= page && len(items) < 50 {
			sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
			return apps, nil
		}
	}
	return nil, ErrDomainProviderConflict
}

func (p *CloudflareDomains) accessApp(ctx context.Context, d api.HostingDomain) (cfDomainApp, error) {
	var app cfDomainApp
	if d.Details.AccessID != "" {
		_, err := p.request(ctx, "GET", p.appPath()+"/"+d.Details.AccessID, nil, &app)
		if err == nil {
			if !p.ownedApp(app, d) {
				return app, ErrDomainProviderConflict
			}
			return app, nil
		}
		if !errors.Is(err, errDomainProviderMissing) {
			return app, err
		}
	}
	apps, err := p.findApps(ctx, d)
	if err != nil {
		return app, err
	}
	if len(apps) > 1 {
		return app, ErrDomainProviderConflict
	}
	if len(apps) == 1 {
		return apps[0], nil
	}
	body := map[string]any{"name": p.appName(d), "domain": d.Hostname, "type": "self_hosted", "session_duration": "1h", "app_launcher_visible": false, "http_only_cookie_attribute": true, "same_site_cookie_attribute": "lax", "options_preflight_bypass": false, "policies": []map[string]any{{"id": p.policy, "precedence": 1}}}
	_, err = p.request(ctx, "POST", p.appPath(), body, &app)
	if err != nil {
		return app, err
	}
	if !p.ownedApp(app, d) {
		return app, ErrDomainProviderConflict
	}
	return app, nil
}

func (p *CloudflareDomains) Ensure(ctx context.Context, d api.HostingDomain, allowCreate bool) (api.HostingDomainDetails, bool, error) {
	details := d.Details
	if err := p.validateZone(ctx, true); err != nil {
		return details, false, err
	}
	host, err := p.findHost(ctx, d)
	if err != nil {
		return details, false, err
	}
	if host == nil {
		if !allowCreate || details.ProviderID != "" {
			return details, false, ErrDomainProviderConflict
		}
		// The reconciler has durably recorded this single create attempt before I/O.
		// Existing matches are conflicts, never candidates for implicit adoption.
		var existing []cfDomainHostname
		pages, listErr := p.request(ctx, "GET", p.hostPath()+"?hostname="+url.QueryEscape(d.Hostname)+"&per_page=50", nil, &existing)
		if listErr != nil {
			return details, false, listErr
		}
		if pages > 1 || len(existing) != 0 {
			return details, false, ErrDomainProviderConflict
		}
		host = &cfDomainHostname{}
		_, err = p.request(ctx, "POST", p.hostPath(), map[string]any{"hostname": d.Hostname, "ssl": map[string]string{"method": "txt", "type": "dv"}}, host)
		if err != nil {
			return details, false, err
		}
		if !p.validHost(*host, d) {
			return details, false, ErrDomainProviderConflict
		}
	}
	details.ProviderID = host.ID

	retryable := host.SSL.Status == "validation_timed_out" || host.SSL.Status == "issuance_timed_out" || host.SSL.Status == "deployment_timed_out" || host.SSL.Status == "expired" || host.Status == "blocked"
	if retryable {
		details.TLSStatus = "failed"
	}
	if now := time.Now().UTC(); retryable && (details.ValidationRetriedAt == nil || now.Sub(*details.ValidationRetriedAt) >= api.HostingDomainValidationRetryInterval) {
		details.ValidationRetriedAt = &now
		_, err = p.request(ctx, "PATCH", p.hostPath()+"/"+host.ID, map[string]any{"ssl": map[string]string{"method": "txt", "type": "dv"}}, host)
		if err != nil {
			return details, false, err
		}
		if !p.validHost(*host, d) {
			return details, false, ErrDomainProviderConflict
		}
	}
	details.ProviderID = host.ID
	details.TLSStatus = "pending"
	details.Records = []api.HostingDNSRecord{}
	add := func(record api.HostingDNSRecord) {
		record.Type = strings.ToUpper(record.Type)
		if record.Type == "TXT" || record.Type == "CNAME" {
			details.Records = append(details.Records, record)
		}
	}
	add(host.Ownership)
	for _, record := range host.SSL.Validation {
		if record.TXTName != "" {
			add(api.HostingDNSRecord{Type: "TXT", Name: record.TXTName, Value: record.TXTValue})
		}
		if record.CNAME != "" {
			add(api.HostingDNSRecord{Type: "CNAME", Name: record.CNAME, Value: record.CNAMETarget})
		}
	}
	if len(details.Records) > api.HostingDomainRecordsMax {
		return d.Details, false, ErrDomainProviderUnavailable
	}
	if strings.Contains(host.SSL.Status, "failed") || strings.Contains(host.SSL.Status, "timed_out") || host.SSL.Status == "expired" {
		details.TLSStatus = "failed"
	}
	if host.Status != "active" || host.SSL.Status != "active" {
		return details, false, nil
	}
	details.TLSStatus = "active"
	d.Details = details
	app, err := p.accessApp(ctx, d)
	if err != nil {
		return details, false, err
	}
	if !ValidDigest(app.AUD) || len(app.Policies) != 1 || app.Policies[0].ID != p.policy {
		return details, false, ErrDomainProviderConflict
	}
	details.AccessID, details.AccessAUD = app.ID, app.AUD
	return details, true, nil
}

// Removal uses the durably bound provider ID. Unknown create results require
// operator recovery even during deletion; hostname matching never proves ownership.
func (p *CloudflareDomains) Remove(ctx context.Context, d api.HostingDomain) error {
	if d.Details.ProviderID == "" && d.Details.CreateStartedAt != nil {
		return ErrDomainProviderConflict
	}
	if err := p.validateZone(ctx, false); err != nil {
		return err
	}
	host, err := p.findHost(ctx, d)
	if err != nil {
		return err
	}
	if host != nil {
		if _, err = p.request(ctx, "DELETE", p.hostPath()+"/"+host.ID, nil, nil); err != nil && !errors.Is(err, errDomainProviderMissing) {
			return err
		}
	}
	apps, err := p.findApps(ctx, d)
	if err != nil {
		return err
	}
	if d.Details.AccessID != "" {
		var known cfDomainApp
		_, err := p.request(ctx, "GET", p.appPath()+"/"+d.Details.AccessID, nil, &known)
		if err != nil && !errors.Is(err, errDomainProviderMissing) {
			return err
		}
		if err == nil {
			if !p.ownedApp(known, d) {
				return ErrDomainProviderConflict
			}
			found := false
			for _, app := range apps {
				found = found || app.ID == known.ID
			}
			if !found {
				apps = append(apps, known)
			}
		}
	}
	for _, app := range apps {
		if _, err = p.request(ctx, "DELETE", p.appPath()+"/"+app.ID, nil, nil); err != nil && !errors.Is(err, errDomainProviderMissing) {
			return err
		}
	}
	return nil
}
