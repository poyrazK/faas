package edgetopology

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

// Preserve unknown provider fields through raw record/zone digests. This
// parser extracts configuration only; opaque fields are never routing proof.
func dnsObject(body []byte) (map[string]json.RawMessage, error) {
	if len(body) < 1 || len(body) > api.RuntimeUpgradeDNSResponseMaxBytes || !boundedDNSJSON(body) {
		return nil, fmt.Errorf("%w: bounded provider JSON required", ErrDNSUnverified)
	}
	var object map[string]json.RawMessage
	if err := ingress.DecodeUniqueJSON(body, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%w: unambiguous provider JSON object required", ErrDNSUnverified)
	}
	return object, nil
}

// Check depth iteratively before the shared unique-key decoder recurses into
// opaque provider metadata. A byte bound alone is not a stack-depth bound.
func boundedDNSJSON(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	depth := 0
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return depth == 0
		}
		if err != nil {
			return false
		}
		if delim, ok := token.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
			} else {
				depth--
			}
			if depth > api.RuntimeUpgradeDNSJSONDepthLimit {
				return false
			}
		}
	}
}

func parseDNSEnvelope(body []byte) (map[string]json.RawMessage, error) {
	envelope, err := dnsObject(body)
	if err != nil {
		return nil, err
	}
	var success bool
	var providerErrors []json.RawMessage
	if json.Unmarshal(envelope["success"], &success) != nil || !success || json.Unmarshal(envelope["errors"], &providerErrors) != nil || providerErrors == nil || len(providerErrors) != 0 || len(envelope["result"]) == 0 || strings.TrimSpace(string(envelope["result"])) == "null" {
		return nil, fmt.Errorf("%w: successful provider envelope required", ErrDNSUnverified)
	}
	return envelope, nil
}

func parseDNSZone(body []byte, expected CloudflareZoneRef) ([]string, error) {
	if _, err := dnsObject(body); err != nil {
		return nil, err
	}
	var zone struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Status      string   `json:"status"`
		Type        string   `json:"type"`
		Paused      *bool    `json:"paused"`
		NameServers []string `json:"name_servers"`
	}
	if json.Unmarshal(body, &zone) != nil || zone.ID != expected.ID || zone.Name != expected.Name || zone.Status != "active" || zone.Type != "full" || zone.Paused == nil || *zone.Paused || len(zone.NameServers) < 1 || len(zone.NameServers) > api.RuntimeUpgradeDNSNameServerLimit {
		return nil, fmt.Errorf("%w: exact active full unpaused zone required", ErrDNSUnverified)
	}
	slices.Sort(zone.NameServers)
	for i, name := range zone.NameServers {
		if !dnsName(name, false) || (i > 0 && zone.NameServers[i-1] == name) {
			return nil, fmt.Errorf("%w: distinct canonical configured nameservers required", ErrDNSUnverified)
		}
	}
	return zone.NameServers, nil
}

func parseDNSPage(envelope map[string]json.RawMessage, requested int) ([]json.RawMessage, int, int, error) {
	var rows []json.RawMessage
	var info struct {
		Page       *int `json:"page"`
		PerPage    *int `json:"per_page"`
		Count      *int `json:"count"`
		TotalCount *int `json:"total_count"`
		TotalPages *int `json:"total_pages"`
	}
	if json.Unmarshal(envelope["result"], &rows) != nil || rows == nil || json.Unmarshal(envelope["result_info"], &info) != nil || info.Page == nil || info.PerPage == nil || info.Count == nil || info.TotalCount == nil || info.TotalPages == nil {
		return nil, 0, 0, fmt.Errorf("%w: complete page metadata required", ErrDNSUnverified)
	}
	total := *info.TotalCount
	if total < 0 || total > api.RuntimeUpgradeDNSRecordLimit || *info.Page != requested || *info.PerPage != api.RuntimeUpgradeDNSPageSize || *info.Count != len(rows) {
		return nil, 0, 0, fmt.Errorf("%w: bounded exact page metadata required", ErrDNSUnverified)
	}
	pages := (total + api.RuntimeUpgradeDNSPageSize - 1) / api.RuntimeUpgradeDNSPageSize
	if total == 0 {
		if requested != 1 || len(rows) != 0 || (*info.TotalPages != 0 && *info.TotalPages != 1) {
			return nil, 0, 0, ErrDNSUnverified
		}
		return rows, 0, 1, nil
	}
	expected := min(api.RuntimeUpgradeDNSPageSize, total-(requested-1)*api.RuntimeUpgradeDNSPageSize)
	if *info.TotalPages != pages || requested > pages || len(rows) != expected {
		return nil, 0, 0, fmt.Errorf("%w: truncated or inconsistent page", ErrDNSUnverified)
	}
	return rows, total, pages, nil
}

func parseDNSRecord(body []byte, zone CloudflareZoneRef) (DNSRecordConfig, error) {
	if _, err := dnsObject(body); err != nil {
		return DNSRecordConfig{}, err
	}
	var record struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Content  string `json:"content"`
		TTL      *int   `json:"ttl"`
		Proxied  *bool  `json:"proxied"`
		ZoneID   string `json:"zone_id"`
		ZoneName string `json:"zone_name"`
	}
	if json.Unmarshal(body, &record) != nil || !dnsProviderID(record.ID) || !dnsName(record.Name, true) || (record.Name != zone.Name && !strings.HasSuffix(record.Name, "."+zone.Name)) || !dnsRecordType(record.Type) || record.TTL == nil || *record.TTL < 1 || *record.TTL > api.RuntimeUpgradeDNSRecordTTLMax || (record.ZoneID != "" && record.ZoneID != zone.ID) || (record.ZoneName != "" && record.ZoneName != zone.Name) {
		return DNSRecordConfig{}, fmt.Errorf("%w: canonical in-zone record required", ErrDNSUnverified)
	}
	out := DNSRecordConfig{ID: record.ID, Name: record.Name, Type: record.Type, ConfigSHA256: configDigest(body), TTL: *record.TTL, Proxied: record.Proxied}
	switch record.Type {
	case "A", "AAAA":
		ip, err := netip.ParseAddr(record.Content)
		if err != nil || ip.String() != record.Content || ip.Zone() != "" || ip.Is4In6() || (ip.Is4() != (record.Type == "A")) || record.Proxied == nil {
			return DNSRecordConfig{}, fmt.Errorf("%w: literal configured address and explicit proxy state required", ErrDNSUnverified)
		}
		out.Target = record.Content
	case "CNAME", "NS":
		target := strings.TrimSuffix(record.Content, ".")
		if !dnsName(target, false) || (record.Type == "CNAME" && record.Proxied == nil) || (record.Type == "NS" && record.Proxied != nil && *record.Proxied) {
			return DNSRecordConfig{}, fmt.Errorf("%w: canonical configured domain target required", ErrDNSUnverified)
		}
		out.Target = target
	}
	return out, nil
}

func dnsProviderID(id string) bool {
	if len(id) != hex.EncodedLen(api.RuntimeUpgradeDNSProviderIDBytes) {
		return false
	}
	body, err := hex.DecodeString(id)
	return err == nil && hex.EncodeToString(body) == id && strings.Trim(id, "0") != ""
}

func dnsAPIToken(token string) bool {
	return token != "" && len(token) <= api.RuntimeUpgradeDNSAPITokenMaxBytes && strings.IndexFunc(token, func(r rune) bool { return r < '!' || r > '~' }) == -1
}

func dnsRecordType(value string) bool {
	return value != "" && len(value) <= api.RuntimeUpgradeDNSRecordTypeMaxBytes && strings.IndexFunc(value, func(r rune) bool { return (r < 'A' || r > 'Z') && (r < '0' || r > '9') }) == -1
}

func dnsName(name string, recordOwner bool) bool {
	if name == "" || len(name) > api.TCPListenerTLSHostnameMaxBytes || strings.ToLower(name) != name {
		return false
	}
	for i, label := range strings.Split(name, ".") {
		if recordOwner && i == 0 && label == "*" {
			continue
		}
		if label == "" || len(label) > api.TCPListenerTLSDNSLabelMaxBytes || label[0] == '-' || label[len(label)-1] == '-' || strings.IndexFunc(label, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && (!recordOwner || r != '_')
		}) != -1 {
			return false
		}
	}
	return true
}
