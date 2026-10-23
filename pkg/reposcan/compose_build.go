package reposcan

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// composeDockerfileBuild reads a source-built service's build.args,
// build.target (ADR-20261019142544) and build.secrets (ADR-20261022185329).
// build is the interpolated build declaration and rawBuild the same mapping
// before interpolation; env is the source's .env, which also supplies
// arguments declared without a value; secrets are the top-level secret
// definitions. Options are nil when the service declares none, or when its
// build context has no Dockerfile and therefore builds with Railpack.
func composeDockerfileBuild(fsys fs.FS, build any, rawBuild map[string]any, env map[string]string, secrets map[string]any, context, dockerfile string) (*api.DockerfileBuildOptions, []string, error) {
	declared, ok := build.(map[string]any)
	if !ok {
		return nil, nil, nil
	}
	_, hasArgs := declared["args"]
	_, hasTarget := declared["target"]
	_, hasSecrets := declared["secrets"]
	if !hasArgs && !hasTarget && !hasSecrets {
		return nil, nil, nil
	}
	args, warnings, err := composeBuildArgs(declared["args"], env)
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, composeBuildArgLiteralWarnings(rawBuild["args"])...)
	buildSecrets, secretWarnings, err := composeBuildSecrets(declared["secrets"], secrets)
	if err != nil {
		return nil, nil, err
	}
	warnings = append(warnings, secretWarnings...)
	options := &api.DockerfileBuildOptions{Args: args, Secrets: buildSecrets}
	if raw, ok := declared["target"]; ok && raw != nil {
		target, isString := raw.(string)
		if !isString {
			return nil, nil, fmt.Errorf("build.target must be a stage name")
		}
		options.Target = strings.TrimSpace(target)
	}
	if err := options.Validate(); err != nil {
		return nil, nil, err
	}
	if options.IsZero() {
		return nil, warnings, nil
	}
	dockerfilePath, found, err := composeBuildDockerfile(fsys, context, dockerfile)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, append(warnings, "build.args, build.target and build.secrets ignored: the build context has no Dockerfile, so it builds with Railpack"), nil
	}
	if options.Target != "" {
		body, err := readValidFile(fsys, dockerfilePath)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", dockerfilePath, err)
		}
		if !slices.Contains(dockerfileStages(string(body)), strings.ToLower(options.Target)) {
			return nil, nil, fmt.Errorf("build.target %q is not a stage in %s; name one declared with FROM ... AS", options.Target, dockerfilePath)
		}
	}
	return options, warnings, nil
}

// composeBuildArgs accepts the map and list forms. A name without a value
// reads env, as Compose reads its project environment; an unset name is left
// out so the Dockerfile's ARG default applies.
func composeBuildArgs(raw any, env map[string]string) (map[string]string, []string, error) {
	args := map[string]string{}
	var unset []string
	add := func(name string, value *string) {
		if value != nil {
			args[name] = *value
			return
		}
		if fromEnv, ok := env[name]; ok {
			args[name] = fromEnv
			return
		}
		delete(args, name)
		unset = append(unset, name)
	}
	switch typed := raw.(type) {
	case nil:
	case map[string]any:
		for _, name := range slices.Sorted(maps.Keys(typed)) {
			if typed[name] == nil {
				add(name, nil)
				continue
			}
			value, ok := composeScalarString(typed[name])
			if !ok {
				return nil, nil, fmt.Errorf("build.args.%s must be a string, number or boolean", name)
			}
			add(name, &value)
		}
	case []any:
		for _, item := range typed {
			entry, ok := item.(string)
			if !ok {
				return nil, nil, fmt.Errorf("build.args list entries must be NAME=value or NAME")
			}
			name, value, hasValue := strings.Cut(entry, "=")
			if hasValue {
				add(name, &value)
			} else {
				add(name, nil)
			}
		}
	default:
		return nil, nil, fmt.Errorf("build.args must be a mapping or a list")
	}
	var warnings []string
	for _, name := range slices.Compact(slices.Sorted(slices.Values(unset))) {
		if _, set := args[name]; !set {
			warnings = append(warnings, fmt.Sprintf("build.args.%s has no value and .env does not set it; the Dockerfile's ARG default applies", name))
		}
	}
	if len(args) == 0 {
		return nil, warnings, nil
	}
	return args, warnings, nil
}

// composeScalarString writes a YAML scalar the way Compose passes it to the
// build: numbers in their shortest form, booleans as true or false.
func composeScalarString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	}
	return "", false
}

// composeBuildArgLiteralWarnings flags $NAME without braces in the declared
// values. The scan interpolates only ${NAME}, so such a value reaches the
// build literally, where docker compose would have substituted it.
func composeBuildArgLiteralWarnings(raw any) []string {
	var values []string
	switch typed := raw.(type) {
	case map[string]any:
		for _, name := range slices.Sorted(maps.Keys(typed)) {
			if value, ok := typed[name].(string); ok {
				values = append(values, name+"="+value)
			}
		}
	case []any:
		for _, item := range typed {
			if value, ok := item.(string); ok {
				values = append(values, value)
			}
		}
	}
	var warnings []string
	for _, entry := range values {
		name, value, _ := strings.Cut(entry, "=")
		if variable := unbracedComposeVariable(value); variable != "" {
			warnings = append(warnings, fmt.Sprintf("build.args.%s passes $%s literally; write ${%s} to read it from .env", name, variable, variable))
		}
	}
	return warnings
}

// unbracedComposeVariable returns the first $NAME reference in value that is
// not written ${NAME} or escaped as $$.
func unbracedComposeVariable(value string) string {
	for index := 0; index < len(value)-1; index++ {
		if value[index] != '$' {
			continue
		}
		if value[index+1] == '$' {
			index++
			continue
		}
		if name := composeVariableName.FindString(value[index+1:]); name != "" {
			return name
		}
	}
	return ""
}

// composeBuildDockerfile resolves the Dockerfile a build uses: the declared
// one, or a Dockerfile at the root of the context, matched without regard to
// case like the builder's framework detection.
func composeBuildDockerfile(fsys fs.FS, context, dockerfile string) (string, bool, error) {
	dir := context
	if dir == "" {
		dir = "."
	}
	if dockerfile != "" {
		return path.Join(dir, dockerfile), true, nil
	}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return "", false, fmt.Errorf("read build context %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), "Dockerfile") {
			return path.Join(dir, entry.Name()), true, nil
		}
	}
	return "", false, nil
}

// dockerfileStages returns the lowercased names of the stages a Dockerfile
// declares with FROM ... AS name. Continuation lines are joined and comment
// lines skipped, as the Dockerfile parser does.
func dockerfileStages(body string) []string {
	var stages []string
	var logical strings.Builder
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if strings.HasPrefix(line, "#") {
			continue
		}
		if continued, ok := strings.CutSuffix(line, `\`); ok {
			logical.WriteString(continued)
			logical.WriteByte(' ')
			continue
		}
		logical.WriteString(line)
		fields := strings.Fields(logical.String())
		logical.Reset()
		if len(fields) >= 4 && strings.EqualFold(fields[0], "FROM") && strings.EqualFold(fields[len(fields)-2], "AS") {
			stages = append(stages, strings.ToLower(fields[len(fields)-1]))
		}
	}
	return stages
}

// composeBuildSecrets reads build.secrets: a secret name, or {source, target}
// where target is the id the Dockerfile mounts. Each source must be a
// top-level secret; one declared with environment: NAME mounts the app secret
// NAME (ADR-20261022185329). File and external secrets are not uploaded, so
// they are left out with a warning.
func composeBuildSecrets(raw any, definitions map[string]any) ([]api.DockerfileBuildSecret, []string, error) {
	if raw == nil {
		return nil, nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("build.secrets must be a list")
	}
	var out []api.DockerfileBuildSecret
	var warnings []string
	for _, entry := range entries {
		var source, id string
		switch typed := entry.(type) {
		case string:
			source = strings.TrimSpace(typed)
			id = source
		case map[string]any:
			source, _ = typed["source"].(string)
			source = strings.TrimSpace(source)
			target, _ := typed["target"].(string)
			id = strings.TrimSpace(target)
			if id == "" {
				id = source
			}
		default:
			return nil, nil, fmt.Errorf("build.secrets entries must be a secret name or {source, target}")
		}
		if source == "" {
			return nil, nil, fmt.Errorf("build.secrets entries must name a secret")
		}
		definition, declared := definitions[source]
		if !declared {
			return nil, nil, fmt.Errorf("build secret %q is not declared under the top-level secrets", source)
		}
		fields, _ := definition.(map[string]any)
		name, _ := fields["environment"].(string)
		if name = strings.TrimSpace(name); name == "" {
			warnings = append(warnings, fmt.Sprintf("build secret %q ignored: only secrets declared with environment: NAME are available to builds; "+
				"declare it that way and set the value with `gregale secrets set NAME`", source))
			continue
		}
		out = append(out, api.DockerfileBuildSecret{ID: id, Secret: name})
	}
	return out, warnings, nil
}
