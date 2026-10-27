package reposcan

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociidentity"
)

// composeProcess reads a prebuilt image service's process overrides
// (ADR-1337): entrypoint, user, working_dir, stop_signal and
// stop_grace_period. Nil means the service overrides none of them.
func composeProcess(s composeCandidate, commandSet bool) (*api.ComposeProcess, error) {
	process := &api.ComposeProcess{}
	if s.Entrypoint != nil {
		entrypoint, err := composeEntrypoint(s.Entrypoint)
		if err != nil {
			return nil, err
		}
		if len(entrypoint) == 0 && !commandSet {
			return nil, fmt.Errorf("an empty entrypoint clears the image's ENTRYPOINT and CMD; also set command")
		}
		process.Entrypoint = &entrypoint
	}
	if s.User != nil {
		user := strings.TrimSpace(fmt.Sprint(s.User))
		if err := ociidentity.ValidateSpec(user); err != nil || user == "" {
			return nil, fmt.Errorf("user must be a name or id, optionally with :group")
		}
		process.User = user
	}
	process.WorkingDir = strings.TrimSpace(s.WorkingDir)
	if raw := strings.TrimSpace(s.StopSignal); raw != "" {
		signal := api.CanonicalComposeStopSignal(raw)
		if signal == "" {
			return nil, fmt.Errorf("stop_signal %q is not delivered by the platform; use one of %s", raw, strings.Join(api.ComposeStopSignals, ", "))
		}
		process.StopSignal = signal
	}
	if s.StopGracePeriod != nil {
		seconds, err := composeGraceSeconds(s.StopGracePeriod)
		if err != nil {
			return nil, err
		}
		process.StopGracePeriodS = &seconds
	}
	if err := process.Validate(); err != nil {
		return nil, err
	}
	if process.IsZero() {
		return nil, nil
	}
	return process, nil
}

// composeEntrypoint accepts the list form and a plain string of
// space-separated words. Quoting and shell syntax in the string form would
// need a shell to interpret, so it is rejected in favour of the list form.
// An empty string or list clears the image's ENTRYPOINT.
func composeEntrypoint(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		if strings.ContainsAny(typed, "\"'`\\$;|&<>(){}*?\n") {
			return nil, fmt.Errorf("entrypoint with quoting or shell syntax must use the list form")
		}
		return strings.Fields(typed), nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			arg, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("entrypoint arguments must be strings")
			}
			out = append(out, arg)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("entrypoint must be a string or argument list")
	}
}

// composeGraceSeconds reads a Compose duration ("30s", "1m30s") rounded up
// to whole seconds, the OCI stop grace period's unit.
func composeGraceSeconds(value any) (int, error) {
	raw, ok := value.(string)
	if !ok {
		return 0, fmt.Errorf("stop_grace_period must be a duration such as 30s")
	}
	duration, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || duration < time.Second {
		return 0, fmt.Errorf("stop_grace_period must be a duration of at least 1s, such as 30s")
	}
	if duration > api.MaxAppManifestStopGracePeriod {
		return 0, fmt.Errorf("stop_grace_period must be at most %s", api.MaxAppManifestStopGracePeriod)
	}
	return int(math.Ceil(duration.Seconds())), nil
}

// composeEnvFileKeys returns the variable names declared by a service's
// env_file entries. Values are dropped exactly as for environment: the
// platform never reads secret values from the source; they are set with
// `gregale env` or secrets. A file missing from the source (commonly a
// git-ignored .env) is a warning, not an error.
func composeEnvFileKeys(fsys fs.FS, value any) ([]string, []string, error) {
	paths, err := composeEnvFilePaths(value)
	if err != nil {
		return nil, nil, err
	}
	var keys, warnings []string
	for _, entry := range paths {
		clean := normalizeComposeRelativePath(entry.path)
		if clean == "" || clean == "." || !fs.ValidPath(clean) || strings.HasPrefix(clean, "../") {
			// Valid Compose that the scan cannot read: the file is outside the
			// uploaded source. Before ADR-1337 env_file was ignored, so this
			// warns instead of failing a project that deploys today.
			warnings = append(warnings, fmt.Sprintf("env_file %q is outside the source and was not read; set its variables with `gregale env set`", entry.path))
			continue
		}
		body, err := readValidFile(fsys, clean)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
			if entry.required {
				warnings = append(warnings, fmt.Sprintf("env_file %s is not in the source; set its variables with `gregale env set`", clean))
			}
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read env_file %s: %w", clean, err)
		}
		fileKeys, problems := envFileKeys(body)
		for _, problem := range problems {
			warnings = append(warnings, fmt.Sprintf("env_file %s %s", clean, problem))
		}
		keys = append(keys, fileKeys...)
	}
	return keys, warnings, nil
}

type composeEnvFile struct {
	path     string
	required bool
}

func composeEnvFilePaths(value any) ([]composeEnvFile, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return []composeEnvFile{{path: typed, required: true}}, nil
	case []any:
		out := make([]composeEnvFile, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				out = append(out, composeEnvFile{path: entry, required: true})
			case map[string]any:
				filePath, _ := entry["path"].(string)
				required := true
				if raw, present := entry["required"]; present {
					flag, ok := raw.(bool)
					if !ok {
						return nil, fmt.Errorf("env_file required must be a boolean")
					}
					required = flag
				}
				if format, _ := entry["format"].(string); format != "" && format != "raw" {
					return nil, fmt.Errorf("env_file format %q is not supported", format)
				}
				out = append(out, composeEnvFile{path: filePath, required: required})
			default:
				return nil, fmt.Errorf("env_file entries must be paths or {path, required} mappings")
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("env_file must be a path or a list")
	}
}

// envFileKeys returns the variable names declared by a Compose env file.
// It follows the dotenv grammar Compose accepts: KEY=value, KEY= (empty),
// KEY alone, an optional "export " prefix, blank lines, # comments, inline
// "# comment" after an unquoted value, and single- or double-quoted values
// with backslash-escaped quotes that may span lines. Only names matter, but
// quoted values are consumed to their closing quote so a multi-line value
// never yields bogus keys. A line it cannot read is reported as a problem
// ("line N: ...") and skipped; Compose files that deployed before stay
// deployable.
func envFileKeys(body []byte) ([]string, []string) {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	var keys, problems []string
	for index := 0; index < len(lines); index++ {
		lineNumber := index + 1
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if key == "" || composeVariableName.FindString(key) != key {
			problems = append(problems, fmt.Sprintf("line %d: invalid variable declaration skipped", lineNumber))
			continue
		}
		keys = append(keys, key)
		value = strings.TrimLeft(value, " \t")
		if value == "" || (value[0] != '"' && value[0] != '\'') {
			continue
		}
		end, closed := envFileQuoteEnd(lines, index, value)
		if !closed {
			problems = append(problems, fmt.Sprintf("line %d: unterminated quoted value; the following lines were read as declarations", lineNumber))
			continue
		}
		index = end
	}
	return keys, problems
}

// envFileQuoteEnd finds the line holding the closing quote of a value that
// opens on line start. value begins with the opening quote.
func envFileQuoteEnd(lines []string, start int, value string) (int, bool) {
	quote := value[0]
	text := value[1:]
	for line := start; ; {
		escaped := false
		for i := 0; i < len(text); i++ {
			switch {
			case escaped:
				escaped = false
			case text[i] == '\\':
				escaped = true
			case text[i] == quote:
				return line, true
			}
		}
		line++
		if line >= len(lines) {
			return start, false
		}
		text = lines[line]
	}
}

// mergeEnvKeys returns the sorted, deduplicated union of environment and
// env_file keys.
func mergeEnvKeys(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range groups {
		for _, key := range group {
			if _, dup := seen[key]; dup || key == "" {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// builtServiceProcess folds a built service's entrypoint override into its
// start command (ADR-20261022160000). A built service's command already
// replaces the image's process, so Docker's entrypoint-then-command argv is
// that command: the entrypoint alone when there is no command (an override
// drops the image CMD), the entrypoint followed by the command otherwise, and
// the command alone for an empty entrypoint. A shell-form command is appended
// as shell words. The remaining process keys stay for imaged.
func builtServiceProcess(process *api.ComposeProcess, command []string, shell bool) ([]string, bool, *api.ComposeProcess) {
	if process == nil || process.Entrypoint == nil {
		return command, shell, process
	}
	entrypoint := *process.Entrypoint
	rest := process.Clone()
	rest.Entrypoint = nil
	if rest.IsZero() {
		rest = nil
	}
	switch {
	case len(entrypoint) == 0:
		return command, shell, rest
	case len(command) == 0:
		return slices.Clone(entrypoint), false, rest
	case !shell:
		return append(slices.Clone(entrypoint), command...), false, rest
	}
	words := make([]string, len(entrypoint))
	for i, word := range entrypoint {
		words[i] = shellWord(word)
	}
	return []string{strings.Join(words, " ") + " " + command[0]}, true, rest
}

// shellWord quotes one argument for a /bin/sh -c start command.
func shellWord(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_@%+=:,./-", r)
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}
