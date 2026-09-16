package reposcan

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var composeVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

// resolveComposeFiles applies the conventional automatic override paired with
// the selected base file. Gregale has no -f selector, so accepting more than
// one possible automatic override would make the signed model ambiguous.
func resolveComposeFiles(fsys fs.FS, base []byte, baseName string) ([]byte, []string, error) {
	if err := rejectComposeMergeTags(base, baseName); err != nil {
		return nil, nil, err
	}
	candidates := composeOverrideCandidates(baseName)
	var (
		override     []byte
		overrideName string
	)
	for _, candidate := range candidates {
		body, err := readValidFile(fsys, candidate)
		switch {
		case err == nil:
			if overrideName != "" {
				return nil, nil, fmt.Errorf("reposcan: %s has multiple automatic Compose overrides (%s and %s)", baseName, overrideName, candidate)
			}
			override = body
			overrideName = candidate
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrInvalid):
			continue
		default:
			return nil, nil, fmt.Errorf("reposcan: read %s: %w", candidate, err)
		}
	}
	if overrideName == "" {
		return base, []string{baseName}, nil
	}
	if err := rejectComposeMergeTags(override, overrideName); err != nil {
		return nil, nil, err
	}

	var baseDoc, overrideDoc map[string]any
	if err := yaml.Unmarshal(base, &baseDoc); err != nil {
		return nil, nil, fmt.Errorf("reposcan: parse %s: %w", baseName, err)
	}
	if err := yaml.Unmarshal(override, &overrideDoc); err != nil {
		return nil, nil, fmt.Errorf("reposcan: parse %s: %w", overrideName, err)
	}
	merged := mergeComposeMap(baseDoc, overrideDoc, "")
	body, err := yaml.Marshal(merged)
	if err != nil {
		return nil, nil, fmt.Errorf("reposcan: resolve %s and %s: %w", baseName, overrideName, err)
	}
	return body, []string{baseName, overrideName}, nil
}

func composeOverrideCandidates(baseName string) []string {
	switch baseName {
	case nameComposeYAML:
		return []string{"compose.override.yaml", "compose.override.yml"}
	case nameComposeYML:
		return []string{"compose.override.yml", "compose.override.yaml"}
	case nameDockerComposeYML:
		return []string{"docker-compose.override.yml", "docker-compose.override.yaml"}
	case nameDockerComposeYAML:
		return []string{"docker-compose.override.yaml", "docker-compose.override.yml"}
	default:
		return nil
	}
}

func rejectComposeMergeTags(body []byte, source string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("reposcan: parse %s: %w", source, err)
	}
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if node.Tag == "!reset" || node.Tag == "!override" {
			return fmt.Errorf("reposcan: %s uses unsupported Compose merge directive %s", source, node.Tag)
		}
		for _, child := range node.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(&doc)
}

func mergeComposeMap(base, override map[string]any, field string) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		if value == nil {
			out[key] = nil
			continue
		}
		baseValue, exists := out[key]
		if !exists {
			out[key] = value
			continue
		}
		baseMap, baseOK := baseValue.(map[string]any)
		overrideMap, overrideOK := value.(map[string]any)
		if baseOK && overrideOK && key != "command" && key != "entrypoint" {
			out[key] = mergeComposeMap(baseMap, overrideMap, key)
			continue
		}
		baseList, baseOK := baseValue.([]any)
		overrideList, overrideOK := value.([]any)
		if baseOK && overrideOK && composeListMerges(field, key) {
			out[key] = appendComposeUnique(baseList, overrideList)
			continue
		}
		out[key] = value
	}
	return out
}

func composeListMerges(parent, field string) bool {
	if parent != "" && parent != "services" {
		return field == "ports" || field == "env_file" || field == "profiles" || field == "depends_on" || field == "environment"
	}
	return false
}

func appendComposeUnique(base, override []any) []any {
	out := append([]any(nil), base...)
	seen := make(map[string]struct{}, len(out))
	for _, value := range out {
		encoded, _ := yaml.Marshal(value)
		seen[string(encoded)] = struct{}{}
	}
	for _, value := range override {
		encoded, _ := yaml.Marshal(value)
		if _, ok := seen[string(encoded)]; ok {
			continue
		}
		seen[string(encoded)] = struct{}{}
		out = append(out, value)
	}
	return out
}

func readComposeEnv(fsys fs.FS) (map[string]string, error) {
	body, err := readValidFile(fsys, ".env")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reposcan: read .env: %w", err)
	}
	values := make(map[string]string)
	for lineNumber, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || composeVariableName.FindString(key) != key {
			return nil, fmt.Errorf("reposcan: .env line %d has an invalid variable declaration", lineNumber+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		} else if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		values[key] = value
	}
	return values, nil
}

func interpolateComposeCandidate(candidate *composeCandidate, values map[string]string, source, service string) error {
	fields := []struct {
		name  string
		value *any
	}{
		{name: "build", value: &candidate.Build},
		{name: "command", value: &candidate.Command},
	}
	for _, field := range fields {
		resolved, err := interpolateComposeValue(*field.value, values, source, service, field.name)
		if err != nil {
			return err
		}
		*field.value = resolved
	}
	ports, err := interpolateComposeValue(candidate.Ports, values, source, service, "ports")
	if err != nil {
		return err
	}
	if ports != nil {
		candidate.Ports, _ = ports.([]any)
	}
	image, err := interpolateComposeString(candidate.Image, values, source, service, "image")
	if err != nil {
		return err
	}
	candidate.Image = image
	return nil
}

func interpolateComposeValue(value any, values map[string]string, source, service, field string) (any, error) {
	switch typed := value.(type) {
	case string:
		return interpolateComposeString(typed, values, source, service, field)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			resolved, err := interpolateComposeValue(item, values, source, service, field)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			resolved, err := interpolateComposeString(item, values, source, service, field)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := interpolateComposeValue(item, values, source, service, field+"."+key)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	default:
		return value, nil
	}
}

func interpolateComposeString(input string, values map[string]string, source, service, field string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(input); {
		if input[index] != '$' {
			out.WriteByte(input[index])
			index++
			continue
		}
		if index+1 < len(input) && input[index+1] == '$' {
			out.WriteByte('$')
			index += 2
			continue
		}
		if index+1 >= len(input) || input[index+1] != '{' {
			out.WriteByte(input[index])
			index++
			continue
		}
		end, ok := composeInterpolationEnd(input, index+2)
		if !ok {
			return "", fmt.Errorf("reposcan: %s: service %s field %s has an unterminated interpolation", source, service, field)
		}
		expression := input[index+2 : end]
		name := composeVariableName.FindString(expression)
		if name == "" {
			return "", fmt.Errorf("reposcan: %s: service %s field %s has an invalid interpolation", source, service, field)
		}
		operator := expression[len(name):]
		value, set := values[name]
		empty := value == ""
		var replacement string
		switch {
		case operator == "":
			if set {
				replacement = value
			}
		case strings.HasPrefix(operator, ":-"):
			if !set || empty {
				replacement = operator[2:]
			} else {
				replacement = value
			}
		case strings.HasPrefix(operator, "-"):
			if !set {
				replacement = operator[1:]
			} else {
				replacement = value
			}
		case strings.HasPrefix(operator, ":?"):
			if !set || empty {
				return "", missingComposeVariable(source, service, field, name)
			}
			replacement = value
		case strings.HasPrefix(operator, "?"):
			if !set {
				return "", missingComposeVariable(source, service, field, name)
			}
			replacement = value
		case strings.HasPrefix(operator, ":+"):
			if set && !empty {
				replacement = operator[2:]
			}
		case strings.HasPrefix(operator, "+"):
			if set {
				replacement = operator[1:]
			}
		default:
			return "", fmt.Errorf("reposcan: %s: service %s field %s uses unsupported interpolation for variable %s", source, service, field, name)
		}
		resolved, err := interpolateComposeString(replacement, values, source, service, field)
		if err != nil {
			return "", err
		}
		out.WriteString(resolved)
		index = end + 1
	}
	return out.String(), nil
}

func composeInterpolationEnd(input string, start int) (int, bool) {
	depth := 1
	for index := start; index < len(input); index++ {
		if input[index] == '$' && index+1 < len(input) && input[index+1] == '{' {
			depth++
			index++
			continue
		}
		if input[index] == '}' {
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}

func missingComposeVariable(source, service, field, variable string) error {
	return fmt.Errorf("reposcan: %s: service %s field %s requires variable %s", source, service, field, variable)
}
