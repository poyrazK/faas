package objectstorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Config contains only non-secret settings and environment variable names.
// Backend IDs are permanent: retain an old entry while buckets reference it.
type Config struct {
	Accounting       *api.ObjectStoragePolicy  `json:"accounting,omitempty"`
	Pricing          *api.ObjectStoragePricing `json:"pricing,omitempty"`
	DefaultRegion    string                    `json:"default_region"`
	Defaults         map[string]string         `json:"defaults"`
	MaxBucketsPerApp int                       `json:"max_buckets_per_app"`
	MaxUploadBytes   int64                     `json:"max_upload_bytes"`
	Backends         []BackendConfig           `json:"backends"`
}

type BackendConfig struct {
	UsageReportsPath string `json:"usage_reports_path,omitempty"`
	ID               string `json:"id"`
	Driver           string `json:"driver"`
	Region           string `json:"region"`
	// Namespace identifies the upstream account/cluster. Changing it, the
	// endpoint or S3 region fences existing buckets instead of misrouting data.
	Namespace       string   `json:"namespace"`
	Endpoint        string   `json:"endpoint"`
	S3Region        string   `json:"s3_region"`
	PathStyle       bool     `json:"path_style"`
	AccessKeyEnv    string   `json:"access_key_env"`
	SecretKeyEnv    string   `json:"secret_key_env"`
	SessionTokenEnv string   `json:"session_token_env,omitempty"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
	AllowHTTP       bool     `json:"allow_http,omitempty"`
}

type Registry struct {
	usageReportPaths map[string]string
	Accounting       api.ObjectStoragePolicy
	Pricing          *api.ObjectStoragePricing
	DefaultRegion    string
	MaxBucketsPerApp int
	MaxUploadBytes   int64
	backends         map[string]Backend
	defaults         map[string]string
}

type Factory func(BackendConfig, func(string) string) (Provider, error)

// NewRegistry accepts driver factories so future provisioners/storage engines
// can plug in without edits to API handlers or persistence.
func NewRegistry(c Config, getenv func(string) string, factories map[string]Factory) (*Registry, error) {
	if c.MaxBucketsPerApp == 0 {
		c.MaxBucketsPerApp = api.DefaultObjectBucketsPerApp
	}
	if c.MaxUploadBytes == 0 {
		c.MaxUploadBytes = api.DefaultObjectUploadBytes
	}
	if c.MaxBucketsPerApp < 1 || c.MaxBucketsPerApp > api.MaxObjectBucketsPerApp || c.MaxUploadBytes < 1 || c.MaxUploadBytes > api.MaxObjectUploadBytes {
		return nil, errors.New("object storage: invalid bucket or upload limit")
	}
	r := &Registry{DefaultRegion: c.DefaultRegion, MaxBucketsPerApp: c.MaxBucketsPerApp, MaxUploadBytes: c.MaxUploadBytes, backends: map[string]Backend{}, defaults: map[string]string{}}
	r.usageReportPaths = map[string]string{}
	if c.Accounting != nil {
		if !c.Accounting.Valid() {
			return nil, errors.New("object storage: invalid accounting policy")
		}
		r.Accounting = *c.Accounting
	}
	if c.Pricing != nil {
		if !c.Pricing.Valid() {
			return nil, errors.New("object storage: invalid pricing")
		}
		pricing := *c.Pricing
		r.Pricing = &pricing
	}
	validID := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	for _, b := range c.Backends {
		if b.UsageReportsPath != "" {
			if !filepath.IsAbs(b.UsageReportsPath) {
				return nil, errors.New("object storage: usage_reports_path must be absolute")
			}
			r.usageReportPaths[b.ID] = b.UsageReportsPath
		}
		if !validID.MatchString(b.ID) || !validID.MatchString(b.Region) || b.Namespace == "" || b.S3Region == "" {
			return nil, errors.New("object storage: invalid backend identity or region")
		}
		if _, ok := r.backends[b.ID]; ok {
			return nil, errors.New("object storage: duplicate backend ID")
		}
		u, err := url.Parse(b.Endpoint)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && (!b.AllowHTTP || u.Scheme != "http")) {
			return nil, fmt.Errorf("object storage: invalid endpoint for backend %s", b.ID)
		}
		for _, origin := range b.AllowedOrigins {
			o, err := url.Parse(origin)
			if err != nil || o.Host == "" || o.User != nil || o.Path != "" || o.RawQuery != "" || o.Fragment != "" || (o.Scheme != "https" && (!b.AllowHTTP || o.Scheme != "http")) {
				return nil, errors.New("object storage: allowed_origins must be explicit origins")
			}
		}
		factory, ok := factories[b.Driver]
		if !ok {
			return nil, fmt.Errorf("object storage: unknown driver %s", b.Driver)
		}
		p, err := factory(b, getenv)
		if err != nil {
			return nil, fmt.Errorf("object storage: backend %s configuration failed: %w", b.ID, err)
		}
		r.backends[b.ID] = Backend{ID: b.ID, Region: b.Region, Fingerprint: fingerprint(b), Provider: p}
	}
	for region, id := range c.Defaults {
		b, ok := r.backends[id]
		if !ok || b.Region != region {
			return nil, errors.New("object storage: default references missing backend or mismatched region")
		}
		r.defaults[region] = id
	}
	if _, ok := r.defaults[r.DefaultRegion]; !ok {
		return nil, errors.New("object storage: default_region is not configured")
	}
	return r, nil
}

// ChargeForUsage returns a customer-facing estimate when the operator has
// configured a rate card. A nil result is intentional when pricing is absent:
// deployments can qualify accounting and safety budgets before choosing
// customer prices.
func (r *Registry) ChargeForUsage(usage api.ObjectStorageUsage) (*api.ObjectStorageCharge, error) {
	if r == nil || r.Pricing == nil {
		return nil, nil
	}
	charge, err := CalculateCharge(*r.Pricing, usage)
	if err != nil {
		return nil, err
	}
	return &charge, nil
}

func Load(getenv func(string) string) (*Registry, error) {
	path := getenv("FAAS_OBJECT_STORAGE_CONFIG")
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:forbidigo // Trusted operator config path from process environment, never a customer path.
	if err != nil {
		return nil, errors.New("object storage: cannot open configuration")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() > 1<<20 {
		return nil, errors.New("object storage: configuration exceeds size limit or cannot be read")
	}
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, errors.New("object storage: invalid configuration JSON")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errors.New("object storage: trailing configuration data")
	}
	return NewRegistry(c, getenv, map[string]Factory{"s3": NewS3})
}

func (r *Registry) Default(region string) (Backend, error) {
	if r == nil {
		return Backend{}, ErrUnavailable
	}
	b, ok := r.backends[r.defaults[region]]
	if !ok {
		return Backend{}, ErrInvalid
	}
	return b, nil
}

func (r *Registry) Resolve(id, placementFingerprint string) (Backend, error) {
	if r == nil {
		return Backend{}, ErrUnavailable
	}
	b, ok := r.backends[id]
	if !ok || b.Fingerprint != placementFingerprint {
		return Backend{}, ErrUnavailable
	}
	return b, nil
}

func (r *Registry) Regions() []string {
	regions := make([]string, 0, len(r.defaults))
	for region := range r.defaults {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions
}
