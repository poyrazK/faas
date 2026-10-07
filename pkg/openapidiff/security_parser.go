package openapidiff

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type securityParser struct {
	spec     *Spec
	work     *securityWork
	row      *SecurityRoute
	revision string
}

func (parser *securityParser) unknown(policy *securityPolicy, code string) {
	policy.complete = false
	parser.work.emit(parser.row, "unknown", code, parser.revision)
}

func (parser *securityParser) policy(operation *Operation) securityPolicy {
	policy := securityPolicy{complete: true, schemes: map[string]securityScheme{}, summary: SecuritySummary{Source: "absent", Authentication: "unknown", CredentialKinds: []string{}}}
	raw, present := operation.Raw["security"]
	if present {
		policy.summary.Source = "operation"
	} else {
		raw, present = parser.spec.Raw["security"]
		if present {
			policy.summary.Source = "root"
		}
	}
	if !present {
		policy.clauses = []securityClause{{}}
		policy.summary.Authentication = "no_declared_requirement"
		return policy
	}
	list, valid := raw.([]any)
	if !valid {
		parser.unknown(&policy, "invalid_security_requirements")
		return policy
	}
	if !parser.work.use(len(list) + 1) {
		policy.complete = false
		return policy
	}
	if len(list) == 0 {
		policy.clauses = []securityClause{{}}
		policy.summary.Authentication = "no_declared_requirement"
		return policy
	}
	kinds := map[string]bool{}
	for _, rawClause := range list {
		value, valid := rawClause.(map[string]any)
		if !valid {
			parser.unknown(&policy, "invalid_security_requirements")
			continue
		}
		if !parser.work.use(len(value)) {
			policy.complete = false
			break
		}
		clause := securityClause{}
		clauseValid := true
		for _, name := range requestSortedKeys(value) {
			if name == "" || !parser.work.metadata(name) {
				parser.unknown(&policy, "invalid_security_metadata")
				clauseValid = false
				continue
			}
			scheme, found := policy.schemes[name]
			if !found {
				var ok bool
				scheme, ok = parser.scheme(&policy, name)
				if !ok {
					clauseValid = false
					continue
				}
				policy.schemes[name] = scheme
			}
			kinds[scheme.kind] = true
			scopes, valid := value[name].([]any)
			if !valid {
				parser.unknown(&policy, "invalid_security_requirements")
				clauseValid = false
				continue
			}
			if !parser.work.use(len(scopes)) {
				policy.complete = false
				return policy
			}
			if len(scopes) > 0 && scheme.kind != "oauth2" && scheme.kind != "openIdConnect" && strings.HasPrefix(parser.spec.version, "3.0.") {
				parser.unknown(&policy, "invalid_security_requirements")
				clauseValid = false
				continue
			}
			clause[name] = map[string]bool{}
			for _, rawScope := range scopes {
				scope, valid := rawScope.(string)
				if !valid || scope == "" || !parser.work.metadata(scope) {
					parser.unknown(&policy, "invalid_security_metadata")
					clauseValid = false
					continue
				}
				if scheme.kind == "oauth2" && !scheme.scopes[scope] {
					parser.unknown(&policy, "undeclared_oauth_scope")
					clauseValid = false
					continue
				}
				clause[name][scope] = true
			}
		}
		// Invalid clauses must never collapse into anonymous alternatives.
		if clauseValid {
			policy.clauses = append(policy.clauses, clause)
		}
	}
	policy.summary.CredentialKinds = requestSortedKeys(kinds)
	if securityAnonymous(policy) {
		policy.summary.Authentication = "anonymous_allowed"
	} else if policy.complete {
		policy.summary.Authentication = "credentials_required"
	}
	return policy
}

func (parser *securityParser) scheme(policy *securityPolicy, name string) (securityScheme, bool) {
	components, _ := parser.spec.Raw["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	raw, found := schemes[name]
	if !found {
		parser.unknown(policy, "unresolved_security_scheme")
		return securityScheme{}, false
	}
	value, ok := parser.resolve(policy, raw, 0, map[string]bool{})
	if !ok {
		return securityScheme{}, false
	}
	kind, _ := value["type"].(string)
	identity := map[string]any{"type": kind}
	scheme := securityScheme{kind: kind}
	switch kind {
	case "apiKey":
		location, _ := value["in"].(string)
		credential, valid := parser.text(value, "name", true)
		if !valid || (location != "header" && location != "query" && location != "cookie") {
			parser.unknown(policy, "invalid_security_scheme")
			return scheme, false
		}
		if (location == "header" || location == "cookie") && !securityToken(credential) {
			parser.unknown(policy, "invalid_security_scheme")
			return scheme, false
		}
		if location == "header" {
			credential = strings.ToLower(credential)
		}
		identity["in"], identity["name"] = location, credential
	case "http":
		mechanism, _ := value["scheme"].(string)
		mechanism = strings.ToLower(mechanism)
		if mechanism != "basic" && mechanism != "bearer" {
			parser.unknown(policy, "unsupported_security_scheme")
			return scheme, false
		}
		identity["scheme"] = mechanism
	case "oauth2":
		flows, scopes, unresolved := parser.flows(value["flows"])
		if unresolved != "" {
			parser.unknown(policy, unresolved)
			return scheme, false
		}
		identity["flows"], scheme.scopes = flows, scopes
	case "openIdConnect":
		endpoint, valid := parser.endpoint(value, "openIdConnectUrl", true)
		if !valid {
			parser.unknown(policy, "invalid_security_scheme")
			return scheme, false
		}
		if !securityAbsoluteEndpoint(endpoint) {
			parser.unknown(policy, "relative_security_endpoint_not_compared")
			return scheme, false
		}
		identity["openIdConnectUrl"] = endpoint
	case "mutualTLS":
		if strings.HasPrefix(parser.spec.version, "3.0.") {
			parser.unknown(policy, "unsupported_security_scheme")
			return scheme, false
		}
	default:
		parser.unknown(policy, "unsupported_security_scheme")
		return scheme, false
	}
	encoded, _ := json.Marshal(identity) // Only bounded strings and maps above.
	scheme.identity = string(encoded)
	return scheme, true
}

func (parser *securityParser) resolve(policy *securityPolicy, raw any, depth int, refs map[string]bool) (map[string]any, bool) {
	if !parser.work.use(1) || depth > api.SecurityCompatibilityMaxDepth {
		parser.work.exceeded = true
		return nil, false
	}
	value, valid := raw.(map[string]any)
	if !valid {
		parser.unknown(policy, "invalid_security_scheme")
		return nil, false
	}
	rawRef, referenced := value["$ref"]
	if !referenced {
		return value, true
	}
	ref, valid := rawRef.(string)
	if !valid || !parser.work.metadata(ref) || !strings.HasPrefix(ref, "#/components/securitySchemes/") || refs[ref] {
		parser.unknown(policy, "unresolved_security_scheme")
		return nil, false
	}
	name, valid := requestDecodePointer(strings.TrimPrefix(ref, "#/components/securitySchemes/"))
	if !valid || strings.Contains(strings.TrimPrefix(ref, "#/components/securitySchemes/"), "/") {
		parser.unknown(policy, "unresolved_security_scheme")
		return nil, false
	}
	components, _ := parser.spec.Raw["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	target, found := schemes[name]
	if !found {
		parser.unknown(policy, "unresolved_security_scheme")
		return nil, false
	}
	refs[ref] = true
	defer delete(refs, ref)
	return parser.resolve(policy, target, depth+1, refs)
}

func (parser *securityParser) text(value map[string]any, key string, required bool) (string, bool) {
	raw, present := value[key]
	if !present {
		return "", !required
	}
	text, valid := raw.(string)
	return text, valid && text != "" && parser.work.metadata(text)
}
func (parser *securityParser) endpoint(value map[string]any, key string, required bool) (string, bool) {
	text, valid := parser.text(value, key, required)
	if !valid || text == "" {
		return text, valid
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Fragment != "" || strings.ContainsAny(text, " \t\r\n") {
		return text, false
	}
	if parsed.IsAbs() && (parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https")) {
		return text, false
	}
	return text, true
}

func securityToken(value string) bool {
	for _, c := range value {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			continue
		}
		return false
	}
	return value != ""
}

func securityAbsoluteEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs()
}

func (parser *securityParser) flows(raw any) (map[string]any, map[string]bool, string) {
	value, valid := raw.(map[string]any)
	if !valid || !parser.work.use(len(value)+1) {
		return nil, nil, "invalid_oauth_flows"
	}
	flows, scopes := map[string]any{}, map[string]bool{}
	for _, name := range requestSortedKeys(value) {
		if strings.HasPrefix(name, "x-") {
			continue
		}
		if name != "implicit" && name != "password" && name != "clientCredentials" && name != "authorizationCode" {
			return nil, nil, "invalid_oauth_flows"
		}
		flow, valid := value[name].(map[string]any)
		if !valid {
			return nil, nil, "invalid_oauth_flows"
		}
		authorization, validAuth := parser.endpoint(flow, "authorizationUrl", name == "implicit" || name == "authorizationCode")
		token, validToken := parser.endpoint(flow, "tokenUrl", name != "implicit")
		refresh, validRefresh := parser.endpoint(flow, "refreshUrl", false)
		if !validAuth || !validToken || !validRefresh {
			return nil, nil, "invalid_oauth_flows"
		}
		for _, endpoint := range []string{authorization, token, refresh} {
			if endpoint != "" && !securityAbsoluteEndpoint(endpoint) {
				return nil, nil, "relative_security_endpoint_not_compared"
			}
		}
		available, valid := flow["scopes"].(map[string]any)
		if !valid || !parser.work.use(len(available)+1) {
			return nil, nil, "invalid_oauth_flows"
		}
		for scope, description := range available {
			if _, valid := description.(string); !valid || scope == "" || !parser.work.metadata(scope) {
				return nil, nil, "invalid_oauth_flows"
			}
			scopes[scope] = true
		}
		flows[name] = map[string]any{"authorizationUrl": authorization, "tokenUrl": token, "refreshUrl": refresh}
	}
	if len(flows) == 0 {
		return nil, nil, "invalid_oauth_flows"
	}
	return flows, scopes, ""
}
