package routerequirements

import (
	"net/url"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

// CandidateInventory uses only the selected captured candidate contract, even
// when a baseline is unavailable. Referenced or unparsed operations invalidate
// the entire inventory so omissions cannot look like complete coverage.
func CandidateInventory(spec *openapidiff.Spec, deployment, digest string) (inventory CoverageInventory) {
	inventory = CoverageInventory{Status: "unavailable", Code: "candidate_inventory_unavailable", Source: "captured_candidate_contract", Deployment: deployment, SHA256: digest}
	defer func() {
		if inventory.Status != "available" {
			inventory.Routes = nil
			inventory.RouteCount = 0
		}
	}()
	if spec == nil || !strings.HasPrefix(spec.OpenAPIVersion(), "3.0.") && !strings.HasPrefix(spec.OpenAPIVersion(), "3.1.") {
		return inventory
	}
	paths, ok := spec.Raw["paths"].(map[string]any)
	if !ok {
		return inventory
	}
	work := &coverageWork{}
	families := map[string]string{}
	for path, value := range paths {
		if !work.use(1) || !work.metadata(path) {
			inventory.Code = "coverage_limit_exceeded"
			return inventory
		}
		if strings.HasPrefix(path, "x-") {
			continue
		}
		item, valid := value.(map[string]any)
		parsed := spec.Paths[path]
		if !valid || parsed == nil || !strings.HasPrefix(path, "/") {
			inventory.Code = "candidate_inventory_incomplete"
			return inventory
		}
		if _, referenced := item["$ref"]; referenced {
			inventory.Code = "candidate_inventory_referenced"
			return inventory
		}
		if family, code := parseFamily(path); code == "" {
			segments := append([]string(nil), family.segments...)
			for i, parameter := range family.parameters {
				if parameter {
					segments[i] = "{}"
				}
			}
			identity := strings.Join(segments, "/")
			if prior, exists := families[identity]; exists && prior != path {
				inventory.Code = "candidate_inventory_ambiguous"
				return inventory
			}
			families[identity] = path
		}
		for method, value := range item {
			if !work.use(1) || !work.metadata(method) {
				inventory.Code = "coverage_limit_exceeded"
				return inventory
			}
			upper := strings.ToUpper(method)
			if !coverageMethod(upper) && upper != "TRACE" {
				if method == "summary" || method == "description" || method == "servers" || method == "parameters" || strings.HasPrefix(method, "x-") {
					continue
				}
				inventory.Code = "candidate_inventory_incomplete"
				return inventory
			}
			operation, valid := value.(map[string]any)
			if !valid || operation == nil || method != strings.ToLower(method) || !coverageMethod(upper) || parsed.Methods[method] == nil {
				inventory.Code = "candidate_inventory_incomplete"
				return inventory
			}
			if _, referenced := operation["$ref"]; referenced {
				inventory.Code = "candidate_inventory_referenced"
				return inventory
			}
			if !rootedServerPaths(spec.Raw, item, operation, work) {
				inventory.Code = "candidate_server_path_unavailable"
				if work.exceeded {
					inventory.Code = "coverage_limit_exceeded"
				}
				return inventory
			}
			inventory.Routes = append(inventory.Routes, CapturedRoute{Method: upper, Path: path})
			if len(inventory.Routes) > api.RouteCoverageMaxInventoryRoutes {
				inventory.Code = "coverage_limit_exceeded"
				inventory.Routes = nil
				return inventory
			}
		}
	}
	if len(inventory.Routes) == 0 {
		inventory.Code = "candidate_inventory_empty"
		return inventory
	}
	sort.Slice(inventory.Routes, func(i, j int) bool {
		return inventory.Routes[i].Method+" "+inventory.Routes[i].Path < inventory.Routes[j].Method+" "+inventory.Routes[j].Path
	})
	inventory.Status, inventory.Code, inventory.RouteCount = "available", "", len(inventory.Routes)
	return inventory
}

// Effective server arrays inherit root -> path -> operation. Until an explicit
// mapping is available, non-root or variable server paths cannot bind declared
// operation keys to gateway paths. No URL is fetched or copied into findings.
func rootedServerPaths(root, path, operation map[string]any, work *coverageWork) bool {
	var raw any
	present := false
	for _, metadata := range []map[string]any{root, path, operation} {
		if servers, exists := metadata["servers"]; exists {
			raw, present = servers, true
		}
	}
	if !present {
		return true
	}
	servers, ok := raw.([]any)
	if !ok || !work.use(len(servers)) {
		return false
	}
	for _, value := range servers {
		server, ok := value.(map[string]any)
		if !ok {
			return false
		}
		location, ok := server["url"].(string)
		if !ok || !work.metadata(location) || location == "" || strings.ContainsAny(location, "{}") {
			return false
		}
		u, err := url.Parse(location)
		if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" {
			return false
		}
		if u.Scheme == "" {
			if u.Host != "" || u.Path != "/" {
				return false
			}
		} else if !strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") || u.Hostname() == "" {
			return false
		}
	}
	return true
}
