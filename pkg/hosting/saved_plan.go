package hosting

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// HostingAPIOrigin binds a plan to an explicit API origin, never a URL from
// which credentials, a path or a redirect can choose the deployment target.
func HostingAPIOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.RawPath != "" || u.Path != "" && u.Path != "/" || u.Opaque != "" {
		return "", errors.New("use an HTTPS API origin without credentials, path, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || net.ParseIP(host).IsLoopback()
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return "", errors.New("API origin requires HTTPS; loopback HTTP is allowed for local tests")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("API origin has an invalid port")
		}
		port = strconv.Itoa(n)
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

// ParseDeploymentPlan accepts one bounded, versioned report. Saved reports are
// user-controlled review inputs, not signed capabilities or admission tokens.
func ParseDeploymentPlan(data []byte) (api.HostingDeploymentPlan, error) {
	var out api.HostingDeploymentPlan
	if len(data) == 0 || len(data) > api.HostingDeploymentPlanMaxBytes {
		return out, errors.New("saved deployment plan is empty or oversized")
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	if !uniquePlanValue(tokens, 0) {
		return out, errors.New("saved plan has invalid, duplicate, noncanonical or deeply nested JSON fields")
	}
	if _, err := tokens.Token(); !errors.Is(err, io.EOF) {
		return out, errors.New("saved deployment plan must contain one JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, errors.New("saved deployment plan has unknown or invalid fields")
	}
	if err := validateSavedPlan(out); err != nil {
		return out, err
	}
	return out, nil
}

func uniquePlanValue(d *json.Decoder, depth int) bool {
	if depth > api.HostingDeploymentPlanJSONDepth {
		return false
	}
	t, err := d.Token()
	if err != nil {
		return false
	}
	delim, compound := t.(json.Delim)
	if !compound {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] || name != strings.ToLower(name) {
				return false
			}
			seen[name] = true
			if !uniquePlanValue(d, depth+1) {
				return false
			}
		}
	case '[':
		for d.More() {
			if !uniquePlanValue(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	end, err := d.Token()
	return err == nil && (delim == '{' && end == json.Delim('}') || delim == '[' && end == json.Delim(']'))
}

func validateSavedPlan(p api.HostingDeploymentPlan) error {
	if p.Version != api.HostingDeploymentPlanVersion {
		return errors.New("saved plan version is unsupported; generate and review a new plan")
	}
	origin, err := HostingAPIOrigin(p.APIOrigin)
	if err != nil || origin != p.APIOrigin || !ValidCIRunID(p.SiteID) || p.BaseReleaseID != "" && !ValidCIRunID(p.BaseReleaseID) || p.ExpectedRevision < 1 || !ValidDigest(p.ManifestFingerprint) {
		return errors.New("saved plan target, revision or manifest fingerprint is invalid")
	}
	if !p.Advisory || p.State != "within_observed_limits" || p.Blockers == nil || len(p.Blockers) != 0 || p.Changes.Files == nil || len(p.Warnings) == 0 || p.StartedAt.IsZero() || p.ObservedAt.Before(p.StartedAt) || p.UsageObservedAt.IsZero() {
		return errors.New("saved plan is incomplete or blocked; generate and review a new plan")
	}
	if p.FileCount < 1 || p.FileCount > api.HostingFilesMax || p.PayloadBytes < 0 || p.PayloadBytes > api.HostingReleaseMaxBytes || p.ManifestBytes < 1 || p.ManifestBytes > api.HostingManifestMaxBytes || p.AdditionalReservedBytes != api.HostingStorageCopies*p.PayloadBytes+api.HostingManifestMaxBytes || len(p.Quotas) != 3 {
		return errors.New("saved plan accounting is invalid")
	}
	resources := []string{"storage_bytes", "pending_uploads", "releases_per_site"}
	limits := []int64{api.HostingAccountStorageMaxBytes, api.HostingAccountPendingUploadsMax, api.HostingReleasesPerSite}
	additional := []int64{p.AdditionalReservedBytes, 1, 1}
	for i, q := range p.Quotas {
		if q.Resource != resources[i] || q.Limit != limits[i] || q.Additional != additional[i] || q.Current < 0 || q.Current > math.MaxInt64-q.Additional || q.Projected != q.Current+q.Additional || q.Projected > q.Limit || q.Blocked {
			return errors.New("saved plan quotas are incomplete, inconsistent or blocked")
		}
	}
	return nil
}

// CheckReviewedDeploymentPlan preserves the reviewed comparison while allowing
// fresh capacity observations to differ. The server still admits the new release.
func CheckReviewedDeploymentPlan(saved, current api.HostingDeploymentPlan) error {
	if err := validateSavedPlan(saved); err != nil {
		return err
	}
	if saved.APIOrigin != current.APIOrigin || saved.SiteID != current.SiteID || saved.ExpectedRevision != current.ExpectedRevision || saved.BaseReleaseID != current.BaseReleaseID {
		return errors.New("API target or production intent changed; generate and review a new plan")
	}
	before, _ := json.Marshal(saved.Changes)
	after, _ := json.Marshal(current.Changes)
	if saved.ManifestFingerprint != current.ManifestFingerprint || saved.FileCount != current.FileCount || saved.PayloadBytes != current.PayloadBytes || saved.ManifestBytes != current.ManifestBytes || saved.AdditionalReservedBytes != current.AdditionalReservedBytes || !bytes.Equal(before, after) {
		return errors.New("build or reviewed comparison changed; generate and review a new plan")
	}
	if current.State != "within_observed_limits" || len(current.Blockers) != 0 {
		return errors.New("current deployment admission is blocked: " + strings.Join(current.Blockers, ", "))
	}
	return nil
}
