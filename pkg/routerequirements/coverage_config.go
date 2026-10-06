package routerequirements

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

// PreviewConfig assigns captured operations to policy groups or public exceptions.
type PreviewConfig = api.RouteRequirementsConfig
type RouteGroup = api.RouteGroup
type PublicRoute = api.RoutePublicException

func ParsePreview(body []byte) (PreviewConfig, error) {
	if len(body) > api.RouteRequirementsMaxBytes {
		return PreviewConfig{}, fmt.Errorf("route requirements exceed %d bytes", api.RouteRequirementsMaxBytes)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	var config PreviewConfig
	if err := decoder.Decode(&config); err != nil {
		return PreviewConfig{}, fmt.Errorf("decode preview route requirements: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return PreviewConfig{}, errors.New("route requirements must contain exactly one YAML document")
	}
	if config.Version == 1 {
		legacy, err := Parse(body)
		return PreviewConfig{Version: 1, Routes: legacy.Routes}, err
	}
	if config.Version != 2 {
		return PreviewConfig{}, errors.New("preview route requirements version must be 1 or 2")
	}
	if len(config.Groups) > api.RouteCoverageMaxGroups || len(config.Routes) > api.RouteRequirementsMaxRoutes || len(config.Public) > api.RouteRequirementsMaxRoutes || len(config.Groups)+len(config.Routes)+len(config.Public) == 0 {
		return PreviewConfig{}, errors.New("route coverage assignments are empty or exceed the supported limits")
	}
	names, routes := map[string]bool{}, map[string]bool{}
	for i := range config.Routes {
		route := &config.Routes[i]
		route.Method = strings.ToUpper(route.Method)
		if err := validateRoute(*route); err != nil {
			return PreviewConfig{}, fmt.Errorf("route %d: %w", i+1, err)
		}
		if _, code := parseFamily(route.Path); code != "" {
			return PreviewConfig{}, fmt.Errorf("route %d needs a canonical concrete path", i+1)
		}
		key := route.Method + " " + route.Path
		if routes[key] || route.Name != "" && names[route.Name] {
			return PreviewConfig{}, errors.New("duplicate route assignment or name")
		}
		routes[key], names[route.Name] = true, true
	}
	for i := range config.Groups {
		group := &config.Groups[i]
		if !coverageName(group.Name) || names[group.Name] {
			return PreviewConfig{}, fmt.Errorf("group %d needs a unique nonempty name within the supported length", i+1)
		}
		names[group.Name] = true
		family, code := parseFamily(group.PathPrefix)
		if code != "" || family.variable || !strings.HasSuffix(group.PathPrefix, "/") {
			return PreviewConfig{}, fmt.Errorf("group %d path_prefix must be a canonical literal path ending in /", i+1)
		}
		if len(group.Methods) == 0 || len(group.Methods) > len(coverageMethods) {
			return PreviewConfig{}, fmt.Errorf("group %d needs explicit supported methods", i+1)
		}
		seen := map[string]bool{}
		for j, method := range group.Methods {
			method = strings.ToUpper(method)
			group.Methods[j] = method
			if seen[method] {
				return PreviewConfig{}, fmt.Errorf("group %d has duplicate methods", i+1)
			}
			seen[method] = true
			if err := validateRoute(Requirement{Method: method, Path: "/", Require: group.Require}); err != nil {
				return PreviewConfig{}, fmt.Errorf("group %d: %w", i+1, err)
			}
		}
	}
	for i := range config.Public {
		exception := &config.Public[i]
		exception.Method = strings.ToUpper(exception.Method)
		_, code := parseFamily(exception.Path)
		key := exception.Method + " " + exception.Path
		if !coverageMethod(exception.Method) || code != "" || strings.TrimSpace(exception.Reason) == "" || len(exception.Reason) > api.RouteCoverageMaxReasonBytes || strings.IndexFunc(exception.Reason, unicode.IsControl) >= 0 {
			return PreviewConfig{}, fmt.Errorf("public exception %d needs a supported method/path and a bounded nonempty reason", i+1)
		}
		if routes[key] {
			return PreviewConfig{}, fmt.Errorf("public exception %d duplicates or conflicts with another exact assignment", i+1)
		}
		routes[key] = true
	}
	return config, nil
}

func coverageName(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= api.RouteCoverageMaxNameBytes && strings.IndexFunc(name, unicode.IsControl) < 0
}

var coverageMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

func coverageMethod(method string) bool {
	for _, supported := range coverageMethods {
		if supported == method {
			return true
		}
	}
	return false
}
