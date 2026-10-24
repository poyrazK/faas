package faas

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// GregaleFlagEvidenceHeader carries bounded application-reported flag evidence
	// to the Gregale gateway. The gateway consumes and removes it.
	GregaleFlagEvidenceHeader = "X-Faas-Flag-Evidence"
	// GregaleFlagCustomerHeader is replaced with verified tenant identity by the
	// Gregale gateway on managed application ingress.
	GregaleFlagCustomerHeader = "X-Faas-Platform-Tenant-Id"
	// GregaleFlagContextHeader carries explicitly used decisions across managed
	// service calls and durable work.
	GregaleFlagContextHeader = "X-Faas-Flag-Context"

	maxFlagEvidence          = 32
	maxFlagEvidenceBytes     = 16 << 10
	maxFlagBundleBytes       = 256 << 10
	maxFlagIdentityBytes     = 16 << 10
	maxFlagIdentityTokenSize = 8192
	maxFlagContextBytes      = 8 << 10
	maxFlagContextHeaderSize = 12 << 10
	maxFlagVersion           = int64(9_007_199_254_740_991)
)

var (
	flagKeyPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	flagUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	// ErrFlagRequestMissing means a flag check ran outside this client's middleware.
	ErrFlagRequestMissing = errors.New("faas: flag checks require GregaleFlags middleware")
	// ErrFlagsClosed is returned when a closed client is asked to refresh or start.
	ErrFlagsClosed = errors.New("faas: GregaleFlags client is closed")
)

// FlagDecision explains the value selected for one flag in a request.
// Value is a bool for boolean flags and a string for variant flags.
type FlagDecision struct {
	Flag          string              `json:"flag"`
	Value         any                 `json:"value"`
	Type          string              `json:"type,omitempty"`
	ConfigVersion int64               `json:"config_version"`
	RuleID        string              `json:"rule_id,omitempty"`
	Reason        string              `json:"reason"`
	Bucket        *int                `json:"bucket,omitempty"`
	RolloutBucket *int                `json:"rollout_bucket,omitempty"`
	Source        string              `json:"source"`
	InheritedFrom *FlagDecisionOrigin `json:"inherited_from,omitempty"`
}

// FlagEvidence records a decision and whether application code entered the
// selected behavior. Evidence is application-reported, not proof of a side effect.
type FlagEvidence struct {
	FlagDecision
	Used bool `json:"used"`
}

// FlagDecisionOrigin identifies the app and environment that made a decision.
type FlagDecisionOrigin struct {
	AppID         string `json:"app_id"`
	EnvironmentID string `json:"environment_id"`
}

// GregaleFlagsOptions configures the server-side runtime client. APIURL must be
// Gregale's HTTPS API base URL. IdentityEndpoint defaults to
// FAAS_WORKLOAD_IDENTITY_ENDPOINT and must be a loopback HTTP URL.
type GregaleFlagsOptions struct {
	APIURL           string
	IdentityEndpoint string
	HTTPClient       *http.Client
	RefreshInterval  time.Duration
	MaxStale         time.Duration
	Timeout          time.Duration
	Now              func() time.Time
}

type runtimeFlagsBundle struct {
	environmentID string
	version       int64
	flags         map[string]runtimeFeatureFlag
	groups        map[string]map[string]struct{}
}

type runtimeFeatureFlag struct {
	key      string
	typ      string
	enabled  bool
	defaultV any
	seed     string
	rules    []runtimeFlagRule
	variants []runtimeFlagVariant
}

type runtimeFlagRule struct {
	id          string
	customers   map[string]struct{}
	group       string
	subjects    map[string]struct{}
	rollout     *int
	rolloutUnit string
	value       any
	valueSet    bool
}

type runtimeFlagVariant struct {
	key    string
	weight int
}

type flagRequestState struct {
	owner    *GregaleFlags
	customer string
	bundle   *runtimeFlagsBundle
	fresh    bool
	evidence map[string]FlagEvidence
	inherit  map[string]flagPropagatedDecision
	mu       sync.Mutex
}

type flagRequestContextKey struct{}
type flagSubjectContextKey struct{}

// GregaleFlags evaluates immutable flag bundles locally and pins one bundle to
// each request. Call Middleware on Gregale ingress handlers before checking flags.
type GregaleFlags struct {
	apiURL      *url.URL
	identityURL *url.URL
	httpClient  *http.Client
	interval    time.Duration
	maxStale    time.Duration
	timeout     time.Duration
	now         func() time.Time
	appID       string

	refreshMu sync.Mutex
	stateMu   sync.RWMutex
	bundle    *runtimeFlagsBundle
	refreshed time.Time

	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewGregaleFlags constructs a runtime client without making network requests.
func NewGregaleFlags(options GregaleFlagsOptions) (*GregaleFlags, error) {
	apiURL, err := url.Parse(options.APIURL)
	if err != nil || apiURL.Scheme != "https" || apiURL.Hostname() == "" || apiURL.User != nil || apiURL.Fragment != "" {
		return nil, errors.New("faas: Flags API requires an HTTPS URL")
	}
	apiURL = apiURL.ResolveReference(&url.URL{Path: "/v1/runtime/flags"})

	identityEndpoint := options.IdentityEndpoint
	if identityEndpoint == "" {
		identityEndpoint = os.Getenv("FAAS_WORKLOAD_IDENTITY_ENDPOINT")
	}
	identityURL, err := url.Parse(identityEndpoint)
	if err != nil || identityURL.Scheme != "http" || identityURL.User != nil || identityURL.Hostname() == "" || identityURL.Path == "" {
		return nil, errors.New("faas: workload identity endpoint must be loopback HTTP")
	}
	switch strings.ToLower(strings.Trim(identityURL.Hostname(), "[]")) {
	case "127.0.0.1", "::1", "localhost":
	default:
		return nil, errors.New("faas: workload identity endpoint must be loopback HTTP")
	}
	query := identityURL.Query()
	query.Set("audience", "gregale:flags")
	identityURL.RawQuery = query.Encode()
	identityURL.Fragment = ""

	interval := options.RefreshInterval
	if interval == 0 {
		interval = 15 * time.Second
	}
	maxStale := options.MaxStale
	if maxStale == 0 {
		maxStale = 60 * time.Second
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	if interval <= 0 || interval > maxStale || maxStale <= 0 || maxStale > 60*time.Second || timeout <= 0 || timeout > 10*time.Second {
		return nil, errors.New("faas: invalid Flags refresh or timeout bounds")
	}

	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		copy := *client
		client = &copy
	}
	// Runtime credentials and configuration must never follow a redirect to a
	// different endpoint. Request contexts provide the bounded timeout.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &GregaleFlags{
		apiURL: apiURL, identityURL: identityURL, httpClient: client,
		interval: interval, maxStale: maxStale, timeout: timeout, now: now,
		appID: strings.ToLower(os.Getenv("FAAS_APP_ID")),
	}, nil
}

// Start performs a best-effort initial refresh and starts bounded background
// refreshes. An unavailable configuration leaves explicit application fallbacks
// available. Pass a process-lifetime context and call Close during shutdown.
func (f *GregaleFlags) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f.lifecycleMu.Lock()
	if f.closed {
		f.lifecycleMu.Unlock()
		return ErrFlagsClosed
	}
	if f.started {
		f.lifecycleMu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	f.started, f.cancel, f.done = true, cancel, done
	go f.refreshLoop(loopCtx, done)
	f.lifecycleMu.Unlock()
	_ = f.Refresh(loopCtx)
	return nil
}

// Close stops background refresh. It is safe to call more than once.
func (f *GregaleFlags) Close() {
	f.lifecycleMu.Lock()
	if f.closed {
		done := f.done
		f.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	f.closed = true
	cancel, done := f.cancel, f.done
	f.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Refresh fetches, validates, and atomically replaces the current runtime bundle.
func (f *GregaleFlags) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f.lifecycleMu.Lock()
	closed := f.closed
	f.lifecycleMu.Unlock()
	if closed {
		return ErrFlagsClosed
	}
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()
	f.lifecycleMu.Lock()
	closed = f.closed
	f.lifecycleMu.Unlock()
	if closed {
		return ErrFlagsClosed
	}
	return f.load(ctx)
}

func (f *GregaleFlags) refreshLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = f.Refresh(ctx)
		}
	}
}

func (f *GregaleFlags) refreshIfStale(ctx context.Context) error {
	if f.isFresh(f.now()) {
		return nil
	}
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()
	if f.isFresh(f.now()) {
		return nil
	}
	f.lifecycleMu.Lock()
	closed := f.closed
	f.lifecycleMu.Unlock()
	if closed {
		return ErrFlagsClosed
	}
	return f.load(ctx)
}

func (f *GregaleFlags) isFresh(now time.Time) bool {
	f.stateMu.RLock()
	defer f.stateMu.RUnlock()
	if f.bundle == nil || f.refreshed.IsZero() {
		return false
	}
	age := now.Round(0).Sub(f.refreshed)
	return age >= 0 && age <= f.maxStale
}

func (f *GregaleFlags) load(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, f.timeout)
	defer cancel()
	identity, err := f.getJSON(ctx, f.identityURL.String(), maxFlagIdentityBytes, nil)
	if err != nil {
		return fmt.Errorf("faas: read workload identity: %w", err)
	}
	var credentials struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(identity, &credentials); err != nil || credentials.AccessToken == "" || len(credentials.AccessToken) > maxFlagIdentityTokenSize || strings.ContainsAny(credentials.AccessToken, "\r\n \t") {
		return errors.New("faas: invalid Flags workload identity")
	}
	apiURL := *f.apiURL
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return fmt.Errorf("faas: create runtime Flags request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	request.Header.Set("X-Faas-Flags-Capabilities", "subject-targeting-v1")
	response, err := f.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("faas: fetch runtime Flags: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("faas: runtime Flags request failed (%s)", response.Status)
	}
	body, err := readBounded(response.Body, maxFlagBundleBytes+1024)
	if err != nil {
		return fmt.Errorf("faas: read runtime Flags: %w", err)
	}
	bundle, err := validateRuntimeBundle(body)
	if err != nil {
		return fmt.Errorf("faas: validate runtime Flags: %w", err)
	}
	f.stateMu.Lock()
	if f.bundle != nil && (bundle.environmentID != f.bundle.environmentID || bundle.version < f.bundle.version) {
		f.stateMu.Unlock()
		return errors.New("faas: Flags configuration scope or version regressed")
	}
	// Strip Go's monotonic component so VM park/restore and wall-clock
	// corrections are reflected in the same freshness check as other SDKs.
	f.bundle, f.refreshed = bundle, f.now().Round(0)
	f.stateMu.Unlock()
	return nil
}

func (f *GregaleFlags) getJSON(ctx context.Context, target string, limit int64, headers http.Header) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	response, err := f.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed (%s)", response.Status)
	}
	return readBounded(response.Body, limit)
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response too large")
	}
	return body, nil
}

type wireFlagsBundle struct {
	EnvironmentID string                     `json:"environment_id"`
	Version       *int64                     `json:"version"`
	Flags         []wireFlag                 `json:"flags"`
	Groups        map[string]json.RawMessage `json:"groups"`
}

type wireFlag struct {
	Key      string          `json:"key"`
	Type     json.RawMessage `json:"type"`
	Enabled  *bool           `json:"enabled"`
	Default  json.RawMessage `json:"default"`
	Seed     string          `json:"seed"`
	Rules    []wireFlagRule  `json:"rules"`
	Variants json.RawMessage `json:"variants"`
}

type wireFlagRule struct {
	ID          string          `json:"id"`
	Customers   json.RawMessage `json:"customers"`
	Group       json.RawMessage `json:"group"`
	Subjects    json.RawMessage `json:"subjects"`
	Rollout     json.RawMessage `json:"rollout"`
	RolloutUnit json.RawMessage `json:"rollout_unit"`
	Value       json.RawMessage `json:"value"`
}

type wireVariant struct {
	Key    string `json:"key"`
	Weight *int   `json:"weight"`
}

func validateRuntimeBundle(raw []byte) (*runtimeFlagsBundle, error) {
	if len(raw) == 0 || len(raw) > maxFlagBundleBytes+1024 {
		return nil, errors.New("invalid Flags bundle size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var wire wireFlagsBundle
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing Flags bundle data")
	}
	if !validFlagUUID(wire.EnvironmentID) || wire.Version == nil || *wire.Version < 0 || *wire.Version > maxFlagVersion || wire.Flags == nil || len(wire.Flags) > 100 || wire.Groups == nil || len(wire.Groups) > 100 {
		return nil, errors.New("invalid Flags bundle")
	}
	bundle := &runtimeFlagsBundle{
		environmentID: wire.EnvironmentID,
		version:       *wire.Version,
		flags:         make(map[string]runtimeFeatureFlag, len(wire.Flags)),
		groups:        make(map[string]map[string]struct{}, len(wire.Groups)),
	}
	for name, rawMembers := range wire.Groups {
		var members []string
		if !validFlagKey(name) || isJSONNull(rawMembers) || json.Unmarshal(rawMembers, &members) != nil || members == nil || !validCustomerIDs(members) {
			return nil, errors.New("invalid Flags group")
		}
		set := make(map[string]struct{}, len(members))
		for _, member := range members {
			set[member] = struct{}{}
		}
		bundle.groups[name] = set
	}
	for _, wireFlag := range wire.Flags {
		flag, err := validateWireFlag(wireFlag, bundle.groups)
		if err != nil {
			return nil, err
		}
		if _, exists := bundle.flags[flag.key]; exists {
			return nil, errors.New("duplicate Flags key")
		}
		bundle.flags[flag.key] = flag
	}
	return bundle, nil
}

func validateWireFlag(wire wireFlag, groups map[string]map[string]struct{}) (runtimeFeatureFlag, error) {
	flag := runtimeFeatureFlag{key: wire.Key, enabled: wire.Enabled != nil && *wire.Enabled, seed: wire.Seed, rules: make([]runtimeFlagRule, 0, len(wire.Rules))}
	if !validFlagKey(wire.Key) || wire.Enabled == nil || wire.Seed == "" || len(wire.Seed) > 128 || strings.ContainsRune(wire.Seed, 0) || wire.Rules == nil || len(wire.Rules) > 32 {
		return runtimeFeatureFlag{}, errors.New("invalid Flags definition")
	}
	flag.typ = "boolean"
	if len(wire.Type) != 0 {
		var configuredType string
		if isJSONNull(wire.Type) || json.Unmarshal(wire.Type, &configuredType) != nil || configuredType != "boolean" && configuredType != "variant" {
			return runtimeFeatureFlag{}, errors.New("invalid Flags type")
		}
		flag.typ = configuredType
	}
	if flag.typ == "boolean" {
		var value bool
		if len(wire.Default) == 0 || isJSONNull(wire.Default) || json.Unmarshal(wire.Default, &value) != nil {
			return runtimeFeatureFlag{}, errors.New("boolean flag requires a boolean default")
		}
		flag.defaultV = value
		if len(wire.Variants) != 0 {
			return runtimeFeatureFlag{}, errors.New("boolean flag cannot define variants")
		}
	} else {
		var variants []wireVariant
		if len(wire.Variants) == 0 || isJSONNull(wire.Variants) || json.Unmarshal(wire.Variants, &variants) != nil {
			return runtimeFeatureFlag{}, errors.New("invalid Flags variants")
		}
		var value string
		if len(wire.Default) == 0 || isJSONNull(wire.Default) || json.Unmarshal(wire.Default, &value) != nil || !validFlagKey(value) || variants == nil || len(variants) < 2 || len(variants) > 16 {
			return runtimeFeatureFlag{}, errors.New("invalid Flags variant default or variants")
		}
		flag.defaultV = value
		variantKeys := make(map[string]struct{}, len(variants))
		weightTotal := 0
		for _, variant := range variants {
			if !validFlagKey(variant.Key) || variant.Weight == nil || *variant.Weight < 0 || *variant.Weight > 10_000 {
				return runtimeFeatureFlag{}, errors.New("invalid Flags variant")
			}
			if _, exists := variantKeys[variant.Key]; exists {
				return runtimeFeatureFlag{}, errors.New("duplicate Flags variant")
			}
			variantKeys[variant.Key] = struct{}{}
			weightTotal += *variant.Weight
			flag.variants = append(flag.variants, runtimeFlagVariant{key: variant.Key, weight: *variant.Weight})
		}
		if weightTotal != 10_000 {
			return runtimeFeatureFlag{}, errors.New("Flags variant weights must total 10000")
		}
		if _, exists := variantKeys[value]; !exists {
			return runtimeFeatureFlag{}, errors.New("Flags variant default is not defined")
		}
	}

	ruleIDs := make(map[string]struct{}, len(wire.Rules))
	variantKeys := make(map[string]struct{}, len(flag.variants))
	for _, variant := range flag.variants {
		variantKeys[variant.key] = struct{}{}
	}
	for _, wireRule := range wire.Rules {
		rule, err := validateWireFlagRule(wireRule, flag.typ, variantKeys, groups)
		if err != nil {
			return runtimeFeatureFlag{}, err
		}
		if _, exists := ruleIDs[rule.id]; exists {
			return runtimeFeatureFlag{}, errors.New("duplicate Flags rule")
		}
		ruleIDs[rule.id] = struct{}{}
		flag.rules = append(flag.rules, rule)
	}
	return flag, nil
}

func validateWireFlagRule(wire wireFlagRule, flagType string, variants map[string]struct{}, groups map[string]map[string]struct{}) (runtimeFlagRule, error) {
	rule := runtimeFlagRule{id: wire.ID}
	if !validFlagKey(wire.ID) {
		return runtimeFlagRule{}, errors.New("invalid Flags rule id")
	}
	if len(wire.Customers) != 0 {
		var customers []string
		if isJSONNull(wire.Customers) || json.Unmarshal(wire.Customers, &customers) != nil || !validCustomerIDs(customers) {
			return runtimeFlagRule{}, errors.New("invalid Flags rule customers")
		}
		if len(customers) != 0 {
			rule.customers = make(map[string]struct{}, len(customers))
			for _, customer := range customers {
				rule.customers[customer] = struct{}{}
			}
		}
	}
	if len(wire.Group) != 0 {
		if isJSONNull(wire.Group) || json.Unmarshal(wire.Group, &rule.group) != nil || !validFlagKey(rule.group) {
			return runtimeFlagRule{}, errors.New("invalid Flags rule group")
		}
		if _, ok := groups[rule.group]; !ok {
			return runtimeFlagRule{}, errors.New("Flags rule references a missing group")
		}
	}
	if len(wire.Subjects) != 0 {
		var subjects []string
		if isJSONNull(wire.Subjects) || json.Unmarshal(wire.Subjects, &subjects) != nil || len(subjects) == 0 || !validFlagSubjectIDs(subjects) {
			return runtimeFlagRule{}, errors.New("invalid Flags rule subjects")
		}
		if len(subjects) != 0 {
			rule.subjects = make(map[string]struct{}, len(subjects))
			for _, subject := range subjects {
				rule.subjects[subject] = struct{}{}
			}
		}
	}
	if len(wire.Rollout) != 0 {
		var rollout int
		if isJSONNull(wire.Rollout) || json.Unmarshal(wire.Rollout, &rollout) != nil || rollout < 0 || rollout > 10_000 {
			return runtimeFlagRule{}, errors.New("invalid Flags rule rollout")
		}
		rule.rollout = &rollout
	}
	if len(wire.RolloutUnit) != 0 {
		if isJSONNull(wire.RolloutUnit) || json.Unmarshal(wire.RolloutUnit, &rule.rolloutUnit) != nil || rule.rolloutUnit != "customer" && rule.rolloutUnit != "subject" || rule.rollout == nil {
			return runtimeFlagRule{}, errors.New("invalid Flags rule rollout unit")
		}
	}
	if (len(rule.subjects) > 0 || rule.rolloutUnit == "subject") && len(rule.customers) == 0 && rule.group == "" {
		return runtimeFlagRule{}, errors.New("subject targeting requires a customer or group constraint")
	}
	if flagType == "boolean" {
		var value bool
		if len(wire.Value) == 0 || isJSONNull(wire.Value) || json.Unmarshal(wire.Value, &value) != nil {
			return runtimeFlagRule{}, errors.New("boolean Flags rule requires a boolean value")
		}
		rule.value, rule.valueSet = value, true
	} else if len(wire.Value) != 0 {
		var value string
		if isJSONNull(wire.Value) || json.Unmarshal(wire.Value, &value) != nil || !validFlagKey(value) {
			return runtimeFlagRule{}, errors.New("invalid Flags rule variant")
		}
		if _, exists := variants[value]; !exists {
			return runtimeFlagRule{}, errors.New("Flags rule selects an unknown variant")
		}
		rule.value, rule.valueSet = value, true
	}
	if len(rule.customers) == 0 && rule.group == "" && len(rule.subjects) == 0 && rule.rollout == nil {
		return runtimeFlagRule{}, errors.New("Flags rule has no targeting constraint")
	}
	return rule, nil
}

func validCustomerIDs(customers []string) bool {
	if len(customers) > 1000 {
		return false
	}
	seen := make(map[string]struct{}, len(customers))
	for _, customer := range customers {
		if !validFlagUUID(customer) {
			return false
		}
		if _, exists := seen[customer]; exists {
			return false
		}
		seen[customer] = struct{}{}
	}
	return true
}

func validFlagSubjectIDs(subjects []string) bool {
	if len(subjects) > 1000 {
		return false
	}
	seen := make(map[string]struct{}, len(subjects))
	for _, subject := range subjects {
		if !validFlagSubjectID(subject) {
			return false
		}
		if _, exists := seen[subject]; exists {
			return false
		}
		seen[subject] = struct{}{}
	}
	return true
}

func validFlagSubjectID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func validFlagKey(value string) bool  { return flagKeyPattern.MatchString(value) }
func validFlagUUID(value string) bool { return flagUUIDPattern.MatchString(value) }
func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func flagBucket(seed, key, customer string) int {
	digest := sha256.Sum256([]byte(seed + "\x00" + key + "\x00" + customer))
	return int(binary.BigEndian.Uint32(digest[:4]) % 10_000)
}

func flagVariantBucket(seed, key, customer string) int {
	digest := sha256.Sum256([]byte(seed + "\x00" + key + "\x00variant\x00" + customer))
	return int(binary.BigEndian.Uint32(digest[:4]) % 10_000)
}

func flagSubjectBucket(seed, key, customer, subject string) int {
	digest := sha256.Sum256([]byte(seed + "\x00" + key + "\x00subject\x00" + customer + "\x00" + subject))
	return int(binary.BigEndian.Uint32(digest[:4]) % 10_000)
}

func flagSubjectVariantBucket(seed, key, customer, subject string) int {
	digest := sha256.Sum256([]byte(seed + "\x00" + key + "\x00variant\x00subject\x00" + customer + "\x00" + subject))
	return int(binary.BigEndian.Uint32(digest[:4]) % 10_000)
}

func evaluateRuntimeBoolean(bundle *runtimeFlagsBundle, key, customer string, fallback bool) FlagDecision {
	return evaluateRuntimeBooleanForSubject(bundle, key, customer, "", fallback)
}

func evaluateRuntimeBooleanForSubject(bundle *runtimeFlagsBundle, key, customer, subject string, fallback bool) FlagDecision {
	decision := FlagDecision{Flag: key, Value: fallback, ConfigVersion: bundleVersion(bundle), Reason: "flag_missing", Source: "fallback"}
	flag, ok := bundle.flags[key]
	if !ok {
		return decision
	}
	if flag.typ != "boolean" {
		decision.Reason = "type_mismatch"
		return decision
	}
	decision.Value, decision.Source, decision.Reason = flag.defaultV, "configuration", "default"
	if !flag.enabled {
		decision.Reason = "disabled"
		return decision
	}
	if customer == "" {
		decision.Reason = "customer_missing"
		return decision
	}
	if !validFlagSubjectID(subject) {
		subject = ""
	}
	subjectMissing := false
	for _, rule := range flag.rules {
		if len(rule.customers) > 0 {
			if _, matches := rule.customers[customer]; !matches {
				continue
			}
		}
		if rule.group != "" {
			if _, matches := bundle.groups[rule.group][customer]; !matches {
				continue
			}
		}
		subjectScoped := len(rule.subjects) > 0 || rule.rolloutUnit == "subject"
		if subjectScoped && subject == "" {
			subjectMissing = true
			continue
		}
		if len(rule.subjects) > 0 {
			if _, matches := rule.subjects[subject]; !matches {
				continue
			}
		}
		if rule.rollout != nil {
			bucket := flagBucket(flag.seed, flag.key, customer)
			if rule.rolloutUnit == "subject" {
				bucket = flagSubjectBucket(flag.seed, flag.key, customer, subject)
			}
			if bucket >= *rule.rollout {
				continue
			}
			decision.Bucket = intPointer(bucket)
		}
		decision.Value, decision.RuleID, decision.Reason = rule.value, rule.id, "rule_match"
		return decision
	}
	if subjectMissing {
		decision.Reason = "subject_missing"
	}
	return decision
}

func evaluateRuntimeVariant(bundle *runtimeFlagsBundle, key, customer, fallback string) FlagDecision {
	return evaluateRuntimeVariantForSubject(bundle, key, customer, "", fallback)
}

func evaluateRuntimeVariantForSubject(bundle *runtimeFlagsBundle, key, customer, subject, fallback string) FlagDecision {
	decision := FlagDecision{Flag: key, Value: fallback, Type: "variant", ConfigVersion: bundleVersion(bundle), Reason: "flag_missing", Source: "fallback"}
	flag, ok := bundle.flags[key]
	if !ok {
		return decision
	}
	if flag.typ != "variant" {
		decision.Reason = "type_mismatch"
		return decision
	}
	decision.Value, decision.Source, decision.Reason = flag.defaultV, "configuration", "default"
	if !flag.enabled {
		decision.Reason = "disabled"
		return decision
	}
	if customer == "" {
		decision.Reason = "customer_missing"
		return decision
	}
	if !validFlagSubjectID(subject) {
		subject = ""
	}
	subjectMissing := false
	for _, rule := range flag.rules {
		if len(rule.customers) > 0 {
			if _, matches := rule.customers[customer]; !matches {
				continue
			}
		}
		if rule.group != "" {
			if _, matches := bundle.groups[rule.group][customer]; !matches {
				continue
			}
		}
		subjectScoped := len(rule.subjects) > 0 || rule.rolloutUnit == "subject"
		if subjectScoped && subject == "" {
			subjectMissing = true
			continue
		}
		if len(rule.subjects) > 0 {
			if _, matches := rule.subjects[subject]; !matches {
				continue
			}
		}
		var rolloutBucket *int
		if rule.rollout != nil {
			bucket := flagBucket(flag.seed, flag.key, customer)
			if rule.rolloutUnit == "subject" {
				bucket = flagSubjectBucket(flag.seed, flag.key, customer, subject)
			}
			if bucket >= *rule.rollout {
				continue
			}
			rolloutBucket = intPointer(bucket)
		}
		if rule.valueSet {
			decision.Value, decision.RuleID, decision.Reason = rule.value, rule.id, "rule_match"
			decision.RolloutBucket = rolloutBucket
			return decision
		}
		bucket := flagVariantBucket(flag.seed, flag.key, customer)
		if subjectScoped {
			bucket = flagSubjectVariantBucket(flag.seed, flag.key, customer, subject)
		}
		decision.Value, decision.RuleID, decision.Reason = chooseRuntimeVariant(flag.variants, bucket), rule.id, "rule_match"
		decision.Bucket, decision.RolloutBucket = intPointer(bucket), rolloutBucket
		return decision
	}
	if subjectMissing {
		decision.Reason = "subject_missing"
	}
	return decision
}

func chooseRuntimeVariant(variants []runtimeFlagVariant, bucket int) string {
	for _, variant := range variants {
		if bucket < variant.weight {
			return variant.key
		}
		bucket -= variant.weight
	}
	return ""
}

func bundleVersion(bundle *runtimeFlagsBundle) int64 {
	if bundle == nil {
		return 0
	}
	return bundle.version
}

func intPointer(value int) *int { return &value }

func cloneFlagDecision(decision FlagDecision) FlagDecision {
	if decision.Bucket != nil {
		decision.Bucket = intPointer(*decision.Bucket)
	}
	if decision.RolloutBucket != nil {
		decision.RolloutBucket = intPointer(*decision.RolloutBucket)
	}
	if decision.InheritedFrom != nil {
		origin := *decision.InheritedFrom
		decision.InheritedFrom = &origin
	}
	return decision
}

// Boolean evaluates a boolean flag for the request carried by ctx. The first
// evaluation of a key is pinned for the rest of that request.
func (f *GregaleFlags) Boolean(ctx context.Context, key string, fallback bool) (FlagDecision, error) {
	if !validFlagKey(key) {
		return FlagDecision{}, errors.New("faas: invalid flag key")
	}
	state, err := f.requestState(ctx)
	if err != nil {
		return FlagDecision{}, err
	}
	subject, _ := ctx.Value(flagSubjectContextKey{}).(string)
	state.mu.Lock()
	defer state.mu.Unlock()
	if prior, ok := state.evidence[key]; ok {
		if _, validType := prior.Value.(bool); validType && prior.Type == "" {
			return cloneFlagDecision(prior.FlagDecision), nil
		}
		return FlagDecision{Flag: key, Value: fallback, ConfigVersion: prior.ConfigVersion, Reason: "type_mismatch", Source: "fallback"}, nil
	}
	propagated, inherited := state.inherit[key]
	decision := propagated.FlagDecision
	if inherited {
		if _, validType := decision.Value.(bool); validType && decision.Type == "" {
			decision.Source = "inherited"
			origin := propagated.Origin
			decision.InheritedFrom = &origin
		} else {
			decision = FlagDecision{Flag: key, Value: fallback, ConfigVersion: bundleVersion(state.bundle), Reason: "type_mismatch", Source: "fallback"}
		}
	} else if state.fresh && state.bundle != nil {
		decision = evaluateRuntimeBooleanForSubject(state.bundle, key, state.customer, subject, fallback)
	} else {
		decision = FlagDecision{Flag: key, Value: fallback, ConfigVersion: bundleVersion(state.bundle), Reason: "configuration_stale", Source: "fallback"}
	}
	state.record(decision)
	return cloneFlagDecision(decision), nil
}

// Variant evaluates a string variant for the request carried by ctx.
func (f *GregaleFlags) Variant(ctx context.Context, key, fallback string) (FlagDecision, error) {
	if !validFlagKey(key) || !validFlagKey(fallback) {
		return FlagDecision{}, errors.New("faas: invalid flag or variant key")
	}
	state, err := f.requestState(ctx)
	if err != nil {
		return FlagDecision{}, err
	}
	subject, _ := ctx.Value(flagSubjectContextKey{}).(string)
	state.mu.Lock()
	defer state.mu.Unlock()
	if prior, ok := state.evidence[key]; ok {
		if _, validType := prior.Value.(string); validType && prior.Type == "variant" {
			return cloneFlagDecision(prior.FlagDecision), nil
		}
		return FlagDecision{Flag: key, Value: fallback, Type: "variant", ConfigVersion: prior.ConfigVersion, Reason: "type_mismatch", Source: "fallback"}, nil
	}
	propagated, inherited := state.inherit[key]
	decision := propagated.FlagDecision
	if inherited {
		if _, validType := decision.Value.(string); validType && decision.Type == "variant" {
			decision.Source = "inherited"
			origin := propagated.Origin
			decision.InheritedFrom = &origin
		} else {
			decision = FlagDecision{Flag: key, Value: fallback, Type: "variant", ConfigVersion: bundleVersion(state.bundle), Reason: "type_mismatch", Source: "fallback"}
		}
	} else if state.fresh && state.bundle != nil {
		decision = evaluateRuntimeVariantForSubject(state.bundle, key, state.customer, subject, fallback)
	} else {
		decision = FlagDecision{Flag: key, Value: fallback, Type: "variant", ConfigVersion: bundleVersion(state.bundle), Reason: "configuration_stale", Source: "fallback"}
	}
	state.record(decision)
	return cloneFlagDecision(decision), nil
}

func (r *flagRequestState) record(decision FlagDecision) {
	if len(r.evidence) < maxFlagEvidence {
		r.evidence[decision.Flag] = FlagEvidence{FlagDecision: cloneFlagDecision(decision)}
	}
}

func (f *GregaleFlags) requestState(ctx context.Context) (*flagRequestState, error) {
	if ctx == nil {
		return nil, ErrFlagRequestMissing
	}
	state, ok := ctx.Value(flagRequestContextKey{}).(*flagRequestState)
	if !ok || state == nil || state.owner != f {
		return nil, ErrFlagRequestMissing
	}
	return state, nil
}

// WithSubject returns a request context that evaluates subject-targeted rules
// for an opaque ID supplied after the application authenticates its user.
// The subject is not included in evidence or the decision propagation header.
func (f *GregaleFlags) WithSubject(ctx context.Context, subjectID string) (context.Context, error) {
	if !validFlagSubjectID(subjectID) {
		return nil, errors.New("faas: invalid opaque subject ID")
	}
	if _, err := f.requestState(ctx); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, flagSubjectContextKey{}, subjectID), nil
}

// Used marks the moment application code enters the selected behavior. It is
// safe for concurrent goroutines that share a request context.
func (f *GregaleFlags) Used(ctx context.Context, key string) error {
	if !validFlagKey(key) {
		return errors.New("faas: invalid flag key")
	}
	state, err := f.requestState(ctx)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	evidence, ok := state.evidence[key]
	if !ok && len(state.evidence) >= maxFlagEvidence {
		return nil
	}
	if !ok {
		return errors.New("faas: evaluate a flag before marking it used")
	}
	evidence.Used = true
	state.evidence[key] = evidence
	return nil
}

// Evidence returns a sorted copy of the request's bounded decision evidence.
func (f *GregaleFlags) Evidence(ctx context.Context) []FlagEvidence {
	state, err := f.requestState(ctx)
	if err != nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	rows := make([]FlagEvidence, 0, len(state.evidence))
	for _, row := range state.evidence {
		row.FlagDecision = cloneFlagDecision(row.FlagDecision)
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b FlagEvidence) int { return strings.Compare(a.Flag, b.Flag) })
	return rows
}

// PropagationHeader returns the bounded context for downstream Gregale service
// calls or queued work. Only decisions marked Used are included.
func (f *GregaleFlags) PropagationHeader(ctx context.Context) string {
	state, err := f.requestState(ctx)
	if err != nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !validFlagUUID(state.customer) {
		return ""
	}
	decisions := make([]flagPropagatedDecision, 0, len(state.evidence))
	for _, evidence := range state.evidence {
		if !evidence.Used {
			continue
		}
		decision := cloneFlagDecision(evidence.FlagDecision)
		var origin FlagDecisionOrigin
		if decision.InheritedFrom != nil {
			origin = *decision.InheritedFrom
		} else if validFlagUUID(f.appID) && state.bundle != nil && validFlagUUID(state.bundle.environmentID) {
			origin = FlagDecisionOrigin{AppID: f.appID, EnvironmentID: state.bundle.environmentID}
		} else {
			continue
		}
		if decision.Source == "inherited" {
			if isFlagFallbackReason(decision.Reason) {
				decision.Source = "fallback"
			} else {
				decision.Source = "configuration"
			}
		}
		decision.InheritedFrom = nil
		decisions = append(decisions, flagPropagatedDecision{FlagDecision: decision, Origin: origin})
	}
	if len(decisions) == 0 {
		return ""
	}
	slices.SortFunc(decisions, func(a, b flagPropagatedDecision) int { return strings.Compare(a.Flag, b.Flag) })
	raw, err := json.Marshal(flagPropagationEnvelope{Version: 1, CustomerID: state.customer, Decisions: decisions})
	if err != nil || len(raw) > maxFlagContextBytes {
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > maxFlagContextHeaderSize {
		return ""
	}
	return encoded
}

func isFlagFallbackReason(reason string) bool {
	switch reason {
	case "flag_missing", "configuration_stale", "type_mismatch":
		return true
	default:
		return false
	}
}
