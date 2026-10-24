package reposcan

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// composeSecretFiles reads a service's secrets: a secret name, or {source,
// target, mode, uid, gid}. Each source must be a top-level secret; one
// declared with environment: NAME becomes the file /run/secrets/<target>
// holding the app secret NAME (ADR-20261023090000). File and external
// secrets are not uploaded, and a target outside /run/secrets is not written,
// so those are left out with a warning.
func composeSecretFiles(raw any, definitions map[string]any) ([]api.SecretFile, []string, error) {
	if raw == nil {
		return nil, nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("secrets must be a list")
	}
	var out []api.SecretFile
	var warnings []string
	for _, entry := range entries {
		var source, target string
		var mode uint32
		switch typed := entry.(type) {
		case string:
			source = strings.TrimSpace(typed)
		case map[string]any:
			source, _ = typed["source"].(string)
			source = strings.TrimSpace(source)
			target, _ = typed["target"].(string)
			target = strings.TrimSpace(target)
			var err error
			if mode, err = composeSecretMode(typed["mode"]); err != nil {
				return nil, nil, fmt.Errorf("secret %q: %w", source, err)
			}
			if _, uid := typed["uid"]; uid {
				warnings = append(warnings, fmt.Sprintf("secret %q: uid ignored; the file is owned by the service's user", source))
			}
			if _, gid := typed["gid"]; gid {
				warnings = append(warnings, fmt.Sprintf("secret %q: gid ignored; the file is owned by the service's user", source))
			}
		default:
			return nil, nil, fmt.Errorf("secrets entries must be a secret name or {source, target, mode}")
		}
		if source == "" {
			return nil, nil, fmt.Errorf("secrets entries must name a secret")
		}
		definition, declared := definitions[source]
		if !declared {
			return nil, nil, fmt.Errorf("secret %q is not declared under the top-level secrets", source)
		}
		fields, _ := definition.(map[string]any)
		name, _ := fields["environment"].(string)
		if name = strings.TrimSpace(name); name == "" {
			warnings = append(warnings, fmt.Sprintf("secret %q ignored: only secrets declared with environment: NAME are written to /run/secrets; "+
				"declare it that way and set the value with `gregale secrets set NAME`", source))
			continue
		}
		file, inDir := composeSecretFileName(source, target)
		if !inDir {
			warnings = append(warnings, fmt.Sprintf("secret %q ignored: target %q is not a file in %s, the only directory secrets are written to", source, target, api.SecretFilesDir))
			continue
		}
		out = append(out, api.SecretFile{Secret: name, Name: file, Mode: mode})
	}
	if err := api.ValidateSecretFiles(out); err != nil {
		return nil, nil, err
	}
	return out, warnings, nil
}

// composeSecretFileName returns the file name under /run/secrets. Compose
// reads a relative target as a name in that directory and an absolute one
// as the path itself.
func composeSecretFileName(source, target string) (string, bool) {
	if target == "" {
		return source, true
	}
	if strings.HasPrefix(target, "/") {
		cleaned := path.Clean(target)
		if path.Dir(cleaned) != api.SecretFilesDir {
			return "", false
		}
		return path.Base(cleaned), true
	}
	if strings.Contains(target, "/") {
		return "", false
	}
	return target, true
}

// composeSecretMode reads mode as YAML decodes it: a number (0440 is octal in
// YAML), or a string of octal digits. Zero keeps Compose's default, 0444.
func composeSecretMode(raw any) (uint32, error) {
	var mode uint64
	switch typed := raw.(type) {
	case nil:
		return 0, nil
	case int:
		if typed < 0 {
			return 0, fmt.Errorf("mode must be octal permission bits such as 0440")
		}
		mode = uint64(typed)
	case uint64:
		mode = typed
	case string:
		digits := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(typed), "0o"), "0O")
		parsed, err := strconv.ParseUint(digits, 8, 32)
		if err != nil {
			return 0, fmt.Errorf("mode %q must be octal permission bits such as 0440", typed)
		}
		mode = parsed
	default:
		return 0, fmt.Errorf("mode must be octal permission bits such as 0440")
	}
	if mode > 0o777 {
		return 0, fmt.Errorf("mode %#o must be permission bits (at most 0777)", mode)
	}
	return uint32(mode), nil
}
