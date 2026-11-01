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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const minimumOVHAccessLogReportAgeSeconds int64 = 2 * 60 * 60

// Config contains only non-secret settings and environment variable names.
// Backend IDs are permanent: retain an old entry while buckets reference it.
type Config struct {
	Accounting        *api.ObjectStoragePolicy  `json:"accounting,omitempty"`
	Pricing           *api.ObjectStoragePricing `json:"pricing,omitempty"`
	DefaultRegion     string                    `json:"default_region"`
	Defaults          map[string]string         `json:"defaults"`
	MaxBucketsPerApp  int                       `json:"max_buckets_per_app"`
	MaxUploadBytes    int64                     `json:"max_upload_bytes"`
	MaxSinglePutBytes int64                     `json:"max_single_put_bytes,omitempty"`
	MaxPartBytes      int64                     `json:"max_part_bytes,omitempty"`
	Transfer          ObjectTransferConfig      `json:"transfer,omitempty"`
	PublicEndpoint    string                    `json:"public_endpoint,omitempty"`
	PublicRegion      string                    `json:"public_region,omitempty"`
	Backends          []BackendConfig           `json:"backends"`
	// Pools lists, per region, the backends that receive new buckets in fill
	// order (ADR-20261029174627). A region without a pool uses its default.
	Pools map[string][]string `json:"pools,omitempty"`
}

type BackendConfig struct {
	ObjectLock       ObjectLockConfig `json:"object_lock,omitempty"`
	Encryption       EncryptionConfig `json:"encryption,omitempty"`
	UsageReportsPath string           `json:"usage_reports_path,omitempty"`
	Usage            UsageConfig      `json:"usage,omitempty"`
	ID               string           `json:"id"`
	Driver           string           `json:"driver"`
	Region           string           `json:"region"`
	// Namespace identifies the upstream account/cluster. Changing it, the
	// endpoint or S3 region fences existing buckets instead of misrouting data.
	Namespace                    string   `json:"namespace"`
	Endpoint                     string   `json:"endpoint"`
	S3Region                     string   `json:"s3_region"`
	PathStyle                    bool     `json:"path_style"`
	OwnedNodeReceipts            bool     `json:"owned_node_receipts,omitempty"`
	OwnedNodeSettlements         bool     `json:"owned_node_settlements,omitempty"`
	OwnedNodeUsageCounters       bool     `json:"owned_node_usage_counters,omitempty"`
	AccessKeyEnv                 string   `json:"access_key_env"`
	SecretKeyEnv                 string   `json:"secret_key_env"`
	SessionTokenEnv              string   `json:"session_token_env,omitempty"`
	GCSLocation                  string   `json:"gcs_location,omitempty"`
	GCSStorageClass              string   `json:"gcs_storage_class,omitempty"`
	GCSServiceAccount            string   `json:"gcs_service_account,omitempty"`
	GCSImpersonateServiceAccount bool     `json:"gcs_impersonate_service_account,omitempty"`
	AllowedOrigins               []string `json:"allowed_origins,omitempty"`
	AllowHTTP                    bool     `json:"allow_http,omitempty"`
	// MaxBuckets caps the live buckets placed on this backend; 0 is no
	// ceiling. Set it below the provider's per-account bucket quota.
	MaxBuckets int64 `json:"max_buckets,omitempty"`
	// GatewayAccessKeyEnv, GatewaySecretKeyEnv and GatewaySessionTokenEnv
	// name the credential the S3 gateways use instead of AccessKeyEnv,
	// SecretKeyEnv and SessionTokenEnv, so a gateway host never holds the
	// credential that creates and deletes buckets (ADR-20261031122757 §4). A
	// backend that names none reaches the provider from the gateways with
	// the admin credential.
	GatewayAccessKeyEnv    string `json:"gateway_access_key_env,omitempty"`
	GatewaySecretKeyEnv    string `json:"gateway_secret_key_env,omitempty"`
	GatewaySessionTokenEnv string `json:"gateway_session_token_env,omitempty"`
	// useGatewayCredential is set by LoadGateway: the backend's provider is
	// built with the gateway credential when it names one.
	useGatewayCredential bool
}

// UsageConfig selects the provider-specific authoritative usage adapter. It
// contains only environment-variable names and safe identity fields; secret
// values never belong in the object-storage registry.
type UsageConfig struct {
	Driver               string `json:"driver,omitempty"`
	ServiceNameEnv       string `json:"service_name_env,omitempty"`
	ApplicationKeyEnv    string `json:"application_key_env,omitempty"`
	ApplicationSecretEnv string `json:"application_secret_env,omitempty"`
	ConsumerKeyEnv       string `json:"consumer_key_env,omitempty"`
	RequestLogBucket     string `json:"request_log_bucket,omitempty"`
	RequestLogPrefix     string `json:"request_log_prefix,omitempty"`
}

type Registry struct {
	usageReportPaths  map[string]string
	Accounting        api.ObjectStoragePolicy
	Pricing           *api.ObjectStoragePricing
	DefaultRegion     string
	MaxBucketsPerApp  int
	MaxUploadBytes    int64
	MaxSinglePutBytes int64
	MaxPartBytes      int64
	Transfer          ObjectTransferConfig
	PublicEndpoint    string
	PublicRegion      string
	backends          map[string]Backend
	defaults          map[string]string
	pools             map[string][]string
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
	if c.MaxSinglePutBytes == 0 {
		c.MaxSinglePutBytes = min(c.MaxUploadBytes, api.MaxObjectSinglePutBytes)
		if c.Transfer.Profile == "proxied" {
			c.MaxSinglePutBytes = min(c.MaxSinglePutBytes, api.MaxObjectProxiedRequestBytes)
		}
	}
	if c.MaxPartBytes == 0 {
		c.MaxPartBytes = min(c.MaxUploadBytes, api.DefaultMultipartPartBytes)
	}
	if c.MaxSinglePutBytes < 1 || c.MaxSinglePutBytes > min(c.MaxUploadBytes, api.MaxObjectSinglePutBytes) || c.MaxPartBytes < 1 || c.MaxPartBytes > min(c.MaxUploadBytes, api.MaxObjectSinglePutBytes) {
		return nil, errors.New("object storage: invalid single PUT or multipart part limit")
	}
	if c.PublicEndpoint == "" {
		c.PublicEndpoint = "https://s3.gregale.dev"
	}
	if c.PublicRegion == "" {
		c.PublicRegion = "us-east-1"
	}
	if c.MaxBucketsPerApp < 1 || c.MaxBucketsPerApp > api.MaxObjectBucketsPerApp || c.MaxUploadBytes < 1 || c.MaxUploadBytes > api.MaxObjectUploadBytes {
		return nil, errors.New("object storage: invalid bucket or upload limit")
	}
	publicEndpoint, err := url.Parse(c.PublicEndpoint)
	if err != nil || publicEndpoint.Scheme != "https" || publicEndpoint.Hostname() == "" || publicEndpoint.User != nil || (publicEndpoint.Path != "" && publicEndpoint.Path != "/") || publicEndpoint.RawPath != "" || publicEndpoint.RawQuery != "" || publicEndpoint.Fragment != "" {
		return nil, errors.New("object storage: public_endpoint must be an HTTPS origin")
	}
	validRegion := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	if !validRegion.MatchString(c.PublicRegion) {
		return nil, errors.New("object storage: invalid public_region")
	}
	r := &Registry{DefaultRegion: c.DefaultRegion, MaxBucketsPerApp: c.MaxBucketsPerApp, MaxUploadBytes: c.MaxUploadBytes, PublicEndpoint: strings.TrimRight(c.PublicEndpoint, "/"), PublicRegion: c.PublicRegion, backends: map[string]Backend{}, defaults: map[string]string{}, pools: map[string][]string{}}
	r.MaxSinglePutBytes, r.MaxPartBytes = c.MaxSinglePutBytes, c.MaxPartBytes
	r.Transfer, err = NormalizeObjectTransfer(c.Transfer, c.MaxSinglePutBytes, c.MaxPartBytes)
	if err != nil {
		return nil, err
	}
	r.usageReportPaths = map[string]string{}
	if c.Accounting != nil {
		if !c.Accounting.Valid() {
			return nil, errors.New("object storage: invalid accounting policy")
		}
		r.Accounting = *c.Accounting
		if r.Accounting.GatewaySafety() && r.Accounting.GatewayMeteringSince.After(time.Now().UTC()) {
			return nil, errors.New("object storage: gateway metering coverage cannot begin in the future")
		}
		// Gateway safety meters stored bytes itself but not provider-side
		// requests or egress, so it prices storage only (ADR-20261024132844).
		if r.Accounting.GatewaySafety() && (c.Pricing != nil && !c.Pricing.StorageOnly() || c.PublicEndpoint == "" || r.Transfer.Profile != "proxied") {
			return nil, errors.New("object storage: gateway safety accounting requires a public gateway, proxied transfers and storage-only pricing")
		}
	}
	if c.Pricing != nil {
		if !c.Pricing.Valid() {
			return nil, errors.New("object storage: invalid pricing")
		}
		pricing := *c.Pricing
		r.Pricing = &pricing
	}
	validID := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	validGCSLocation := regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)
	validGCSServiceAccount := regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,126}@[a-z0-9.-]+\.gserviceaccount\.com$`)
	encryptionOwners := map[string]string{}
	configs := map[string]BackendConfig{}
	for _, b := range c.Backends {
		if b.MaxBuckets < 0 || b.MaxBuckets > api.MaxObjectStorageBackendBuckets {
			return nil, fmt.Errorf("object storage: backend %s has an invalid max_buckets", b.ID)
		}
		if b.OwnedNodeUsageCounters && !b.OwnedNodeReceipts {
			return nil, errors.New("object storage: owned node usage counters require owned node receipts")
		}
		if b.OwnedNodeSettlements && !b.OwnedNodeReceipts {
			return nil, errors.New("object storage: owned node settlements require owned node receipts")
		}
		if b.OwnedNodeReceipts && (b.Driver != "s3" || b.ObjectLock.Enabled || len(b.Encryption.Algorithms) != 0 || len(b.Encryption.Keys) != 0) {
			return nil, errors.New("object storage: owned node receipts require an S3 backend without encryption or Object Lock")
		}
		if b.UsageReportsPath != "" {
			if !filepath.IsAbs(b.UsageReportsPath) {
				return nil, errors.New("object storage: usage_reports_path must be absolute")
			}
			r.usageReportPaths[b.ID] = b.UsageReportsPath
		}
		if !validID.MatchString(b.ID) || !validID.MatchString(b.Region) || b.Namespace == "" {
			return nil, errors.New("object storage: invalid backend identity or region")
		}
		if _, ok := r.backends[b.ID]; ok {
			return nil, errors.New("object storage: duplicate backend ID")
		}
		if b.Usage.Driver != "" && b.Usage.Driver != "ovh" {
			return nil, fmt.Errorf("object storage: backend %s has an unknown usage driver", b.ID)
		}
		if b.Usage.Driver == "ovh" {
			if b.Driver != "s3" || b.UsageReportsPath == "" || b.Usage.ApplicationKeyEnv == "" || b.Usage.ApplicationSecretEnv == "" || b.Usage.ConsumerKeyEnv == "" || b.Usage.RequestLogBucket == "" {
				return nil, fmt.Errorf("object storage: backend %s has incomplete ovh usage configuration", b.ID)
			}
			if c.Accounting != nil && c.Accounting.MaxReportAgeSeconds < minimumOVHAccessLogReportAgeSeconds {
				return nil, fmt.Errorf("object storage: backend %s requires max_report_age_seconds >= %d for delayed OVH access logs", b.ID, minimumOVHAccessLogReportAgeSeconds)
			}
		}
		switch b.Driver {
		case "s3":
			if b.S3Region == "" {
				return nil, fmt.Errorf("object storage: backend %s requires an S3 region", b.ID)
			}
			if b.GCSLocation != "" || b.GCSStorageClass != "" || b.GCSServiceAccount != "" || b.GCSImpersonateServiceAccount {
				return nil, fmt.Errorf("object storage: backend %s mixes S3 and GCS settings", b.ID)
			}
		case "gcs":
			for _, algorithm := range b.Encryption.Algorithms {
				if algorithm != "AES256" {
					return nil, fmt.Errorf("object storage: backend %s only supports GCS managed AES256 encryption", b.ID)
				}
			}
			if !validGCSLocation.MatchString(b.GCSLocation) || !validGCSServiceAccount.MatchString(b.GCSServiceAccount) {
				return nil, fmt.Errorf("object storage: backend %s requires a valid GCS location and service account", b.ID)
			}
			if b.S3Region != "" || b.PathStyle || b.AccessKeyEnv != "" || b.SecretKeyEnv != "" || b.SessionTokenEnv != "" || b.hasGatewayCredential() {
				return nil, fmt.Errorf("object storage: backend %s mixes GCS and S3 settings", b.ID)
			}
			switch b.GCSStorageClass {
			case "", "STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE":
			default:
				return nil, fmt.Errorf("object storage: backend %s has an invalid GCS storage class", b.ID)
			}
		}
		if b.Endpoint != "" {
			u, err := url.Parse(b.Endpoint)
			if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && (!b.AllowHTTP || u.Scheme != "http")) {
				return nil, fmt.Errorf("object storage: invalid endpoint for backend %s", b.ID)
			}
		} else if b.Driver == "s3" {
			return nil, fmt.Errorf("object storage: backend %s requires an endpoint", b.ID)
		}
		for _, origin := range b.AllowedOrigins {
			o, err := url.Parse(origin)
			if err != nil || o.Host == "" || o.User != nil || o.Path != "" || o.RawQuery != "" || o.Fragment != "" || (o.Scheme != "https" && (!b.AllowHTTP || o.Scheme != "http")) {
				return nil, errors.New("object storage: allowed_origins must be explicit origins")
			}
		}
		if (b.GatewayAccessKeyEnv == "") != (b.GatewaySecretKeyEnv == "") || (b.GatewaySessionTokenEnv != "" && b.GatewayAccessKeyEnv == "") {
			return nil, fmt.Errorf("object storage: backend %s must name both gateway credential variables", b.ID)
		}
		if b.GatewayAccessKeyEnv != "" && (b.GatewayAccessKeyEnv == b.AccessKeyEnv || b.GatewaySecretKeyEnv == b.SecretKeyEnv) {
			return nil, fmt.Errorf("object storage: backend %s must name a gateway credential apart from its admin credential", b.ID)
		}
		gatewayCredential := b.useGatewayCredential && b.GatewayAccessKeyEnv != ""
		if gatewayCredential {
			b.AccessKeyEnv, b.SecretKeyEnv, b.SessionTokenEnv = b.GatewayAccessKeyEnv, b.GatewaySecretKeyEnv, b.GatewaySessionTokenEnv
		}
		factory, ok := factories[b.Driver]
		if !ok {
			return nil, fmt.Errorf("object storage: unknown driver %s", b.Driver)
		}
		if err = validateObjectLockConfig(b.ObjectLock, b.Driver); err != nil {
			return nil, err
		}
		b.Encryption, err = normalizeEncryptionConfig(b.Encryption, b, c.PublicRegion)
		if err != nil {
			return nil, err
		}
		for _, key := range b.Encryption.Keys {
			identity := b.Driver + "\x00" + key.ProviderKeyID
			if owner, ok := encryptionOwners[identity]; ok && owner != key.Reference {
				return nil, fmt.Errorf("object storage: backend %s has an ambiguous encryption key owner", b.ID)
			}
			encryptionOwners[identity] = key.Reference
		}
		p, err := factory(b, getenv)
		if err != nil {
			return nil, fmt.Errorf("object storage: backend %s configuration failed: %w", b.ID, err)
		}
		if len(b.Encryption.Algorithms) != 0 {
			if _, ok := p.(ObjectEncryptionProvider); !ok {
				return nil, fmt.Errorf("object storage: backend %s lacks its declared encryption capability", b.ID)
			}
		}
		if b.ObjectLock.Enabled && !SupportsNativeObjectLock(p) {
			return nil, fmt.Errorf("object storage: backend %s lacks its declared Object Lock contract", b.ID)
		}
		r.backends[b.ID] = Backend{ObjectLock: b.ObjectLock, Encryption: cloneEncryptionConfig(b.Encryption), AllowedOrigins: append([]string(nil), b.AllowedOrigins...), ID: b.ID, Region: b.Region, Namespace: b.Namespace, Fingerprint: fingerprint(b), Provider: p, UsageReportsPath: b.UsageReportsPath, Usage: b.Usage, MaxBuckets: b.MaxBuckets, GatewayCredential: gatewayCredential}
		configs[b.ID] = b
	}
	for region, id := range c.Defaults {
		b, ok := r.backends[id]
		if !ok || b.Region != region {
			return nil, errors.New("object storage: default references missing backend or mismatched region")
		}
		r.defaults[region] = id
		r.pools[region] = []string{id}
	}
	for region, ids := range c.Pools {
		if err := validatePool(region, ids, c.Defaults, configs); err != nil {
			return nil, err
		}
		r.defaults[region] = ids[0]
		r.pools[region] = append([]string(nil), ids...)
	}
	if _, ok := r.defaults[r.DefaultRegion]; !ok {
		return nil, errors.New("object storage: default_region is not configured")
	}
	return r, nil
}

// Backends returns a stable snapshot of configured placements. The returned
// values contain provider interfaces but never provider credentials.
func (r *Registry) Backends() []Backend {
	if r == nil {
		return nil
	}
	out := make([]Backend, 0, len(r.backends))
	for _, backend := range r.backends {
		backend.Encryption = cloneEncryptionConfig(backend.Encryption)
		backend.AllowedOrigins = append([]string(nil), backend.AllowedOrigins...)
		out = append(out, backend)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ChargeForUsage returns a customer-facing estimate when the operator has
// configured a rate card. A nil result is intentional when pricing is absent:
// deployments can qualify accounting and safety budgets before choosing
// customer prices. The allowance is the account plan's included usage.
func (r *Registry) ChargeForUsage(usage api.ObjectStorageUsage, allowance api.ObjectStorageAllowance) (*api.ObjectStorageCharge, error) {
	// Gateway safety charges once Gregale meters every live bucket's storage
	// (ADR-20261024132844); until then there is no estimate.
	if r == nil || r.Pricing == nil || r.Accounting.GatewaySafety() && !usage.Chargeable() {
		return nil, nil
	}
	charge, err := CalculateCharge(*r.Pricing, usage, allowance)
	if err != nil {
		return nil, err
	}
	return &charge, nil
}

func Load(getenv func(string) string) (*Registry, error) {
	return load(getenv, false)
}

// LoadGateway loads the registry for the S3 gateways: s3-gatewayd and
// gatewayd-public's public reads. Each backend that names a gateway
// credential is reached with it instead of the bucket-admin credential apid
// uses (ADR-20261031122757 §4).
func LoadGateway(getenv func(string) string) (*Registry, error) {
	return load(getenv, true)
}

func (b BackendConfig) hasGatewayCredential() bool {
	return b.GatewayAccessKeyEnv != "" || b.GatewaySecretKeyEnv != "" || b.GatewaySessionTokenEnv != ""
}

func load(getenv func(string) string, gateway bool) (*Registry, error) {
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
	if err := validateLoadedOwnedNodes(c, getenv); err != nil {
		return nil, err
	}
	for i := range c.Backends {
		c.Backends[i].useGatewayCredential = gateway
	}
	return NewRegistry(c, getenv, map[string]Factory{"gcs": NewGCS, "s3": NewS3})
}

func (r *Registry) Default(region string) (Backend, error) {
	if r == nil {
		return Backend{}, ErrUnavailable
	}
	b, ok := r.backends[r.defaults[region]]
	if !ok {
		return Backend{}, ErrInvalid
	}
	b.Encryption = cloneEncryptionConfig(b.Encryption)
	b.AllowedOrigins = append([]string(nil), b.AllowedOrigins...)
	return b, nil
}

// CanProvision reports whether a new bucket in region has the operator-side
// accounting configuration needed for signed URLs. It does not claim that the
// provider report or inventory is fresh; admission checks that separately.
// Existing buckets must remain accessible for cleanup when this is false.
func (r *Registry) CanProvision(region string) bool {
	if r == nil || !r.Accounting.Valid() {
		return false
	}
	backends, err := r.Placements(region)
	if err != nil {
		return false
	}
	for _, backend := range backends {
		if !r.Accounting.GatewaySafety() && backend.UsageReportsPath == "" {
			return false
		}
	}
	return true
}

// CanProvisionAllRegions is the conservative customer-facing availability
// signal: every advertised placement must have an accounting feed configured.
func (r *Registry) CanProvisionAllRegions() bool {
	if r == nil || len(r.defaults) == 0 {
		return false
	}
	for region := range r.defaults {
		if !r.CanProvision(region) {
			return false
		}
	}
	return true
}

func (r *Registry) Resolve(id, placementFingerprint string) (Backend, error) {
	if r == nil {
		return Backend{}, ErrUnavailable
	}
	b, ok := r.backends[id]
	if !ok || b.Fingerprint != placementFingerprint {
		return Backend{}, ErrUnavailable
	}
	b.Encryption = cloneEncryptionConfig(b.Encryption)
	b.AllowedOrigins = append([]string(nil), b.AllowedOrigins...)
	return b, nil
}

// Placements returns the backends that receive new buckets in region, in
// fill order (ADR-20261029174627). A region without a pool has its default.
func (r *Registry) Placements(region string) ([]Backend, error) {
	if r == nil {
		return nil, ErrUnavailable
	}
	ids, ok := r.pools[region]
	if !ok {
		return nil, ErrInvalid
	}
	out := make([]Backend, 0, len(ids))
	for _, id := range ids {
		b := r.backends[id]
		b.Encryption = cloneEncryptionConfig(b.Encryption)
		b.AllowedOrigins = append([]string(nil), b.AllowedOrigins...)
		out = append(out, b)
	}
	return out, nil
}

// PlacementLimited reports whether a new bucket in region needs the state
// store's placement check: its pool has several backends or a ceiling.
func (r *Registry) PlacementLimited(region string) bool {
	backends, err := r.Placements(region)
	return err == nil && (len(backends) > 1 || backends[0].MaxBuckets > 0)
}

func (r *Registry) Regions() []string {
	regions := make([]string, 0, len(r.defaults))
	for region := range r.defaults {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions
}
