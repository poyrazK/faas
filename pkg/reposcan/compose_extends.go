package reposcan

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeLinkedFilesMax bounds the Compose files one scan loads through
// include and extends, so a repository cannot fan the scan out without limit.
const composeLinkedFilesMax = 32

// composeLinker resolves top-level include and service extends into one
// Compose document (ADR-20261019190527). Every service it returns has its
// build context and env_file paths relative to the repository root and its
// extends already merged, so the rest of the scan reads it like a service
// written in the root file.
type composeLinker struct {
	fsys     fs.FS
	loaded   int
	files    map[string]map[string]any
	resolved map[string]map[string]any
	sources  []string
	warnings []string
}

// resolveComposeLinks applies include and extends to the merged root
// document. sources names the files already merged (the root file and its
// override); files loaded here are appended to it.
func resolveComposeLinks(fsys fs.FS, body []byte, sources []string) ([]byte, []string, []string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: parse %s: %w", strings.Join(sources, " + "), err)
	}
	container := doc
	if _, ok := doc["services"]; !ok {
		if wrapped, ok := doc["compose"].(map[string]any); ok {
			container = wrapped
		}
	}
	_, hasInclude := container["include"]
	if !hasInclude && !composeServicesExtend(container) {
		return body, sources, nil, nil
	}
	linker := &composeLinker{
		fsys:     fsys,
		files:    map[string]map[string]any{},
		resolved: map[string]map[string]any{},
		sources:  append([]string(nil), sources...),
	}
	root := sources[0]
	services, err := linker.resolveServices(root, composeServices(container))
	if err != nil {
		return nil, nil, nil, err
	}
	origin := make(map[string]string, len(services))
	for name := range services {
		origin[name] = root
	}
	if err := linker.include(root, container["include"], services, origin, []string{root}); err != nil {
		return nil, nil, nil, err
	}
	delete(container, "include")
	container["services"] = services
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reposcan: resolve include and extends in %s: %w", root, err)
	}
	return out, linker.sources, linker.warnings, nil
}

func composeServices(container map[string]any) map[string]any {
	services, _ := container["services"].(map[string]any)
	if services == nil {
		return map[string]any{}
	}
	return services
}

func composeServicesExtend(container map[string]any) bool {
	for _, service := range composeServices(container) {
		if typed, ok := service.(map[string]any); ok {
			if _, extends := typed["extends"]; extends {
				return true
			}
		}
	}
	return false
}

// load reads, checks and caches one linked Compose file.
func (l *composeLinker) load(file string) (map[string]any, error) {
	if doc, ok := l.files[file]; ok {
		return doc, nil
	}
	if l.loaded >= composeLinkedFilesMax {
		return nil, fmt.Errorf("reposcan: more than %d Compose files are linked through include and extends", composeLinkedFilesMax)
	}
	body, err := readValidFile(l.fsys, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reposcan: Compose file %s does not exist in the source", file)
	}
	if err != nil {
		return nil, fmt.Errorf("reposcan: read %s: %w", file, err)
	}
	if err := rejectComposeMergeTags(body, file); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("reposcan: parse %s: %w", file, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	l.loaded++
	l.files[file] = doc
	l.sources = append(l.sources, file)
	return doc, nil
}

// resolveServices returns every service of one file with extends applied and
// paths rebased to the repository root.
func (l *composeLinker) resolveServices(file string, services map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(services))
	for _, name := range slices.Sorted(maps.Keys(services)) {
		service, err := l.resolveService(file, name, services, nil)
		if err != nil {
			return nil, err
		}
		out[name] = service
	}
	return out, nil
}

func (l *composeLinker) resolveService(file, name string, services map[string]any, chain []string) (map[string]any, error) {
	key := file + "#" + name
	if done, ok := l.resolved[key]; ok {
		return cloneComposeMap(done), nil
	}
	if slices.Contains(chain, key) {
		return nil, fmt.Errorf("reposcan: Compose extends cycle: %s", strings.Join(append(chain, key), " -> "))
	}
	raw, ok := services[name]
	if !ok {
		return nil, fmt.Errorf("reposcan: %s does not define service %q", file, name)
	}
	declared, ok := raw.(map[string]any)
	if !ok {
		if raw != nil {
			return nil, fmt.Errorf("reposcan: %s: service %q must be a mapping", file, name)
		}
		declared = map[string]any{}
	}
	service := cloneComposeMap(declared)
	extends, hasExtends := service["extends"]
	delete(service, "extends")
	normalizeComposeService(service)
	rebaseComposeService(service, path.Dir(file))
	if hasExtends {
		baseFile, baseName, err := composeExtendsTarget(file, extends)
		if err != nil {
			return nil, fmt.Errorf("reposcan: %s: service %q: %w", file, name, err)
		}
		baseServices := services
		if baseFile != file {
			doc, err := l.load(baseFile)
			if err != nil {
				return nil, err
			}
			baseServices = composeServices(doc)
		}
		if _, defined := baseServices[baseName]; !defined {
			return nil, fmt.Errorf("reposcan: %s: service %q extends %q, which %s does not define", file, name, baseName, baseFile)
		}
		base, err := l.resolveService(baseFile, baseName, baseServices, append(chain, key))
		if err != nil {
			return nil, err
		}
		service = mergeComposeMap(base, service, name)
	}
	l.resolved[key] = service
	return cloneComposeMap(service), nil
}

// composeExtendsTarget reads extends: base or {service, file}. A file is
// relative to the declaring file and must stay inside the source.
func composeExtendsTarget(file string, extends any) (string, string, error) {
	switch typed := extends.(type) {
	case string:
		if name := strings.TrimSpace(typed); name != "" {
			return file, name, nil
		}
	case map[string]any:
		name, _ := typed["service"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			break
		}
		other, _ := typed["file"].(string)
		if strings.TrimSpace(other) == "" {
			return file, name, nil
		}
		target, err := composeLinkedPath(file, other)
		if err != nil {
			return "", "", fmt.Errorf("extends file %q: %w", other, err)
		}
		return target, name, nil
	}
	return "", "", errors.New("extends must name a service")
}

// composeLinkedPath resolves a path written in file to a path inside the
// source, relative to the repository root.
func composeLinkedPath(file, written string) (string, error) {
	written = strings.TrimSpace(written)
	if strings.Contains(written, "://") || strings.HasPrefix(written, "git@") {
		return "", errors.New("remote Compose files are not supported; commit the file to the repository")
	}
	if path.IsAbs(written) {
		return "", errors.New("must be a path inside the source")
	}
	target := path.Join(path.Dir(file), written)
	if target == "." || !fs.ValidPath(target) || strings.HasPrefix(target, "../") {
		return "", errors.New("must be a path inside the source")
	}
	return target, nil
}

// include adds the services of each included file. Like Compose, a service
// name defined by two files is an error rather than a merge.
func (l *composeLinker) include(file string, raw any, services map[string]any, origin map[string]string, chain []string) error {
	if raw == nil {
		return nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("reposcan: %s: include must be a list", file)
	}
	for _, entry := range entries {
		paths, options, err := composeIncludeEntry(entry)
		if err != nil {
			return fmt.Errorf("reposcan: %s: %w", file, err)
		}
		if len(paths) == 0 {
			continue
		}
		var targets []string
		remote := false
		for _, written := range paths {
			target, err := composeLinkedPath(file, written)
			if err != nil {
				if strings.Contains(written, "://") || strings.HasPrefix(strings.TrimSpace(written), "git@") {
					l.warnings = append(l.warnings, fmt.Sprintf("reposcan: %s: WARNING include %s not loaded: remote Compose files are not supported; commit the file to the repository", file, written))
					remote = true
					break
				}
				return fmt.Errorf("reposcan: %s: include %q: %w", file, written, err)
			}
			targets = append(targets, target)
		}
		if remote {
			continue
		}
		if len(options) > 0 {
			l.warnings = append(l.warnings, fmt.Sprintf("reposcan: %s: include %s: %s not applied; paths resolve from the included file and variables come from the root .env",
				file, strings.Join(targets, ", "), strings.Join(options, " and ")))
		}
		for _, target := range targets {
			if slices.Contains(chain, target) {
				return fmt.Errorf("reposcan: Compose include cycle: %s", strings.Join(append(chain, target), " -> "))
			}
		}
		included, err := l.includedProject(targets, chain)
		if err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(included)) {
			if previous, taken := origin[name]; taken {
				return fmt.Errorf("reposcan: service %q is defined in both %s and %s; rename one of them", name, previous, targets[0])
			}
			origin[name] = targets[0]
			services[name] = included[name]
		}
	}
	return nil
}

// includedProject loads one include entry: its files merged in order, its
// own includes, and its extends, with paths rebased to the repository root.
func (l *composeLinker) includedProject(targets []string, chain []string) (map[string]any, error) {
	var merged map[string]any
	for _, target := range targets {
		doc, err := l.load(target)
		if err != nil {
			return nil, err
		}
		if merged == nil {
			merged = cloneComposeMap(doc)
			continue
		}
		merged = mergeComposeMap(merged, doc, "")
	}
	first := targets[0]
	if dir := path.Dir(first); dir != "." {
		if info, err := fs.Stat(l.fsys, path.Join(dir, ".env")); err == nil && !info.IsDir() {
			l.warnings = append(l.warnings, fmt.Sprintf("reposcan: include %s: %s is not read; variables come from the root .env", first, path.Join(dir, ".env")))
		}
	}
	services, err := l.resolveServices(first, composeServices(merged))
	if err != nil {
		return nil, err
	}
	origin := make(map[string]string, len(services))
	for name := range services {
		origin[name] = first
	}
	if err := l.include(first, merged["include"], services, origin, append(chain, targets...)); err != nil {
		return nil, err
	}
	return services, nil
}

// composeIncludeEntry reads a short (path) or long ({path, ...}) include. It
// returns the options Gregale does not apply.
func composeIncludeEntry(entry any) ([]string, []string, error) {
	switch typed := entry.(type) {
	case string:
		return []string{typed}, nil, nil
	case map[string]any:
		var paths []string
		switch value := typed["path"].(type) {
		case string:
			paths = []string{value}
		case []any:
			for _, item := range value {
				written, ok := item.(string)
				if !ok {
					return nil, nil, errors.New("include path entries must be strings")
				}
				paths = append(paths, written)
			}
		default:
			return nil, nil, errors.New("include entries need a path")
		}
		var options []string
		for _, option := range []string{"project_directory", "env_file"} {
			if _, set := typed[option]; set {
				options = append(options, option)
			}
		}
		return paths, options, nil
	}
	return nil, nil, errors.New("include entries must be a path or a mapping with a path")
}

// normalizeComposeService writes the short forms Compose normalizes before
// merging in their long form, so an extending service's build or environment
// merges with its base's instead of replacing it: build: dir becomes
// {context: dir}, and an environment list becomes a mapping.
func normalizeComposeService(service map[string]any) {
	if build, ok := service["build"].(string); ok && strings.TrimSpace(build) != "" {
		service["build"] = map[string]any{"context": build}
	}
	if environment, ok := service["environment"].([]any); ok {
		mapped := make(map[string]any, len(environment))
		for _, entry := range environment {
			written, ok := entry.(string)
			if !ok {
				return
			}
			if name, value, hasValue := strings.Cut(written, "="); hasValue {
				mapped[name] = value
			} else {
				mapped[written] = nil
			}
		}
		service["environment"] = mapped
	}
}

// rebaseComposeService rewrites the build context and env_file paths a
// service declares relative to dir so they are relative to the repository
// root. Absolute and remote paths are left for the scan's own validation.
func rebaseComposeService(service map[string]any, dir string) {
	if dir == "." || dir == "" {
		return
	}
	rebase := func(written string) string {
		trimmed := strings.TrimSpace(written)
		if trimmed == "" || path.IsAbs(trimmed) || strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "git@") {
			return written
		}
		return path.Join(dir, trimmed)
	}
	switch build := service["build"].(type) {
	case string:
		service["build"] = rebase(build)
	case map[string]any:
		context, _ := build["context"].(string)
		if strings.TrimSpace(context) == "" {
			context = "."
		}
		build = cloneComposeMap(build)
		build["context"] = rebase(context)
		service["build"] = build
	}
	switch envFile := service["env_file"].(type) {
	case string:
		service["env_file"] = rebase(envFile)
	case []any:
		rebased := make([]any, len(envFile))
		for i, entry := range envFile {
			switch typed := entry.(type) {
			case string:
				rebased[i] = rebase(typed)
			case map[string]any:
				typed = cloneComposeMap(typed)
				if written, ok := typed["path"].(string); ok {
					typed["path"] = rebase(written)
				}
				rebased[i] = typed
			default:
				rebased[i] = entry
			}
		}
		service["env_file"] = rebased
	}
}

// cloneComposeMap copies a decoded mapping deeply enough that merging and
// rebasing never write through to a cached file.
func cloneComposeMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneComposeValue(value)
	}
	return out
}

func cloneComposeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneComposeMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneComposeValue(item)
		}
		return out
	default:
		return value
	}
}
