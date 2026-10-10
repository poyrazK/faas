// Package routerequirements checks customer-owned route requirements against
// current configuration. It never sends requests or changes policy (ADR-436).
package routerequirements

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

type Config = api.RouteRequirementsConfig

type Requirement = api.RouteRequirement

type Checks = api.RouteChecks

type ThrottleRequirement = api.RouteThrottleRequirement

type BudgetRequirement = api.RouteBudgetRequirement

// Parse accepts one strict YAML (or JSON) document. Concrete paths avoid
// presenting one sampled parameter value as coverage of an entire template.
func Parse(body []byte) (Config, error) {
	if len(body) > api.RouteRequirementsMaxBytes {
		return Config{}, fmt.Errorf("route requirements exceed %d bytes", api.RouteRequirementsMaxBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	var legacy struct {
		Version int           `yaml:"version"`
		Routes  []Requirement `yaml:"routes"`
	}
	if err := decoder.Decode(&legacy); err != nil {
		return Config{}, fmt.Errorf("decode route requirements: %w", err)
	}
	config := Config{Version: legacy.Version, Routes: legacy.Routes}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("route requirements must contain exactly one YAML document")
	}
	if config.Version != 1 {
		return Config{}, errors.New("route requirements version must be 1")
	}
	if len(config.Routes) == 0 || len(config.Routes) > api.RouteRequirementsMaxRoutes {
		return Config{}, fmt.Errorf("route requirements must contain 1..%d routes", api.RouteRequirementsMaxRoutes)
	}
	seen, names := map[string]bool{}, map[string]bool{}
	for i := range config.Routes {
		route := &config.Routes[i]
		route.Method = strings.ToUpper(route.Method)
		if err := validateRoute(*route); err != nil {
			return Config{}, fmt.Errorf("route %d: %w", i+1, err)
		}
		key := route.Method + " " + route.Path
		if seen[key] || (route.Name != "" && names[route.Name]) {
			return Config{}, fmt.Errorf("route %d duplicates a method/path or name", i+1)
		}
		seen[key], names[route.Name] = true, true
	}
	return config, nil
}

func validateRoute(route Requirement) error {
	switch route.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
	default:
		return errors.New("method must be GET, HEAD, POST, PUT, PATCH, DELETE, or OPTIONS")
	}
	u, err := url.ParseRequestURI(route.Path)
	if err != nil || u.IsAbs() || u.Path != route.Path || u.RawQuery != "" || u.ForceQuery ||
		!strings.HasPrefix(route.Path, "/") || strings.HasPrefix(route.Path, "//") || len(route.Path) > 2048 ||
		strings.ContainsAny(route.Path, "?#{ }*[]\\") || strings.IndexFunc(route.Path, unicode.IsControl) >= 0 {
		return errors.New("path must be a concrete decoded path without parameters, globs, query, fragment, spaces, or controls")
	}
	for _, segment := range strings.Split(route.Path, "/") {
		if segment == "." || segment == ".." || strings.HasPrefix(segment, ":") {
			return errors.New("path must not contain dot segments or :parameter placeholders")
		}
	}
	if len(route.Name) > 128 || strings.IndexFunc(route.Name, unicode.IsControl) >= 0 {
		return errors.New("name must be at most 128 bytes without control characters")
	}
	checks := route.Require
	if checks.Authentication == "" && checks.Throttle == nil && checks.Budget == nil {
		return errors.New("require must specify authentication, throttle, or budget")
	}
	switch checks.Authentication {
	case "", "consumer", "jwt", "application":
	default:
		return errors.New("authentication must be consumer, jwt, or application")
	}
	if t := checks.Throttle; t != nil {
		switch t.KeyBy {
		case api.ThrottleKeyByNone, api.ThrottleKeyByAPIKey, api.ThrottleKeyByConsumerID, api.ThrottleKeyByJWTSubject,
			api.ThrottleKeyByCountry, api.ThrottleKeyByIP:
		default:
			return errors.New("throttle.key_by must be none, api_key, consumer_id, jwt_subject, country, or ip")
		}
		if t.MaxRPS != nil && (*t.MaxRPS <= 0 || math.IsNaN(*t.MaxRPS) || math.IsInf(*t.MaxRPS, 0)) {
			return errors.New("throttle.max_rps must be a finite positive value when set")
		}
		if t.MissingKeyPolicy != "" && t.MissingKeyPolicy != api.ThrottleMissingKeyShared && t.MissingKeyPolicy != api.ThrottleMissingKeyReject {
			return errors.New("throttle.missing_key_policy must be shared or reject")
		}
		if t.KeyBy == api.ThrottleKeyByNone && t.MissingKeyPolicy != "" {
			return errors.New("throttle.missing_key_policy requires a dimensional key_by")
		}
	}
	if b := checks.Budget; b != nil {
		if (b.MaxMS == nil && !b.Explicit) || (b.MaxMS != nil && (*b.MaxMS <= 0 || *b.MaxMS > api.RequestBudgetMax.Milliseconds())) {
			return errors.New("budget needs explicit: true or a positive max_ms within the platform request-budget ceiling")
		}
	}
	return nil
}
