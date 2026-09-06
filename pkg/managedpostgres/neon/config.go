// Package neon implements the managedpostgres provider contract against the
// Neon control-plane API. Vendor vocabulary and wire types stay in this
// package so Gregale's catalog and public API remain provider-neutral.
package neon

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const (
	apiBaseURL                    = "https://console.neon.tech/api/v2"
	apiKeySecret                  = "api-key"
	settingRegionID               = "region_id"
	settingDatabaseName           = "database_name"
	settingMaxStorageBytes        = "max_storage_bytes"
	settingMaxRestoreWindow       = "max_restore_window_seconds"
	maximumNeonRestoreWindow      = int64(30 * 24 * time.Hour / time.Second)
	defaultCredentialPollInterval = 250 * time.Millisecond
)

var (
	validOrganizationID = regexp.MustCompile(`^org-[a-z0-9-]{1,56}$`)
	validRegionID       = regexp.MustCompile(`^aws-[a-z0-9-]{1,59}$`)
	validDatabaseName   = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	validProviderID     = regexp.MustCompile(`^[a-z0-9-]{1,60}$`)
)

type classProfile struct {
	minimumCU float64
	maximumCU float64
}

var profiles = map[managedpostgres.ServiceClass]classProfile{
	managedpostgres.ClassDevelopment: {minimumCU: 0.25, maximumCU: 1},
	managedpostgres.ClassBurstable:   {minimumCU: 0.25, maximumCU: 2},
	managedpostgres.ClassProduction:  {minimumCU: 1, maximumCU: 4},
}

// Provider is a Neon-backed implementation of managedpostgres.Provider.
// Project IDs and endpoint hosts are treated as opaque/sensitive outside the
// adapter and are never included in returned errors.
type Provider struct {
	baseURL                *url.URL
	httpClient             *http.Client
	apiKey                 string
	organizationID         string
	logicalRegion          string
	regionID               string
	databaseName           string
	maxStorageBytes        int64
	maxRestoreWindow       int64
	credentialPollInterval time.Duration
}

// New constructs the production Neon driver from a provider-neutral backend
// entry. Namespace is the Neon organization ID. Secrets are resolved only
// through secret_env and never copied into the placement fingerprint.
func New(config managedpostgres.BackendConfig, getenv func(string) string) (managedpostgres.Provider, error) {
	if getenv == nil || !validOrganizationID.MatchString(config.Namespace) {
		return nil, errors.New("neon: invalid organization configuration")
	}
	if len(config.SecretEnv) != 1 || config.SecretEnv[apiKeySecret] == "" {
		return nil, errors.New("neon: api-key secret mapping is required")
	}
	apiKey := getenv(config.SecretEnv[apiKeySecret])
	if apiKey == "" {
		return nil, errors.New("neon: configured API credential is unavailable")
	}
	parsed, err := parseSettings(config.Settings)
	if err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(apiBaseURL)
	if err != nil {
		return nil, errors.New("neon: invalid API endpoint")
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return newProvider(config.Region, config.Namespace, apiKey, baseURL, client, parsed), nil
}

type settings struct {
	regionID         string
	databaseName     string
	maxStorageBytes  int64
	maxRestoreWindow int64
}

func parseSettings(values map[string]string) (settings, error) {
	allowed := map[string]struct{}{
		settingRegionID: {}, settingDatabaseName: {},
		settingMaxStorageBytes: {}, settingMaxRestoreWindow: {},
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			return settings{}, errors.New("neon: unknown backend setting")
		}
	}
	parsed := settings{regionID: values[settingRegionID], databaseName: values[settingDatabaseName]}
	if parsed.databaseName == "" {
		parsed.databaseName = "gregale"
	}
	if !validRegionID.MatchString(parsed.regionID) || !validDatabaseName.MatchString(parsed.databaseName) {
		return settings{}, errors.New("neon: invalid region or database setting")
	}
	var err error
	parsed.maxStorageBytes, err = positiveSetting(values, settingMaxStorageBytes)
	if err != nil {
		return settings{}, err
	}
	parsed.maxRestoreWindow, err = nonnegativeSetting(values, settingMaxRestoreWindow)
	if err != nil || parsed.maxRestoreWindow > maximumNeonRestoreWindow {
		return settings{}, errors.New("neon: invalid restore-window setting")
	}
	return parsed, nil
}

func positiveSetting(values map[string]string, key string) (int64, error) {
	value, ok := values[key]
	if !ok {
		return 0, errors.New("neon: required capacity setting is missing")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("neon: invalid capacity setting")
	}
	return parsed, nil
}

func nonnegativeSetting(values map[string]string, key string) (int64, error) {
	value, ok := values[key]
	if !ok {
		return 0, errors.New("neon: required restore-window setting is missing")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("neon: invalid restore-window setting")
	}
	return parsed, nil
}

func newProvider(logicalRegion, organizationID, apiKey string, baseURL *url.URL, client *http.Client, parsed settings) *Provider {
	return &Provider{
		baseURL:                baseURL,
		httpClient:             client,
		apiKey:                 apiKey,
		organizationID:         organizationID,
		logicalRegion:          logicalRegion,
		regionID:               parsed.regionID,
		databaseName:           parsed.databaseName,
		maxStorageBytes:        parsed.maxStorageBytes,
		maxRestoreWindow:       parsed.maxRestoreWindow,
		credentialPollInterval: defaultCredentialPollInterval,
	}
}

var _ managedpostgres.Provider = (*Provider)(nil)
