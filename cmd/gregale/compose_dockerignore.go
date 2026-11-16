package main

import (
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeServiceDockerignorePatterns returns the .dockerignore rules of each
// Compose service's build context, scoped to that context, for a project
// archive packed from the Compose root. Docker reads a service's
// .dockerignore from its own context, but the project packer read only the
// root one, so repositories that keep node_modules, build output or test data
// under src/<service>/ (and ignore them there) uploaded all of it and could
// pass the source entry limit (2026-11-15 microservices-demo feedback).
//
// Each pattern is anchored at its context, as Docker anchors it. The
// context's Dockerfile, its parent directories inside the context and its
// .dockerignore are re-included afterwards: Docker always sends them, and the
// builder needs them. readFile reads a file by archive path; missing or
// unreadable files read as absent. Rules come before .gregaleignore, which
// still has the last word.
func composeServiceDockerignorePatterns(readFile func(rel string) ([]byte, bool)) []gregaleignorePattern {
	var compose []byte
	for _, name := range composeFileNames {
		if data, ok := readFile(name); ok {
			compose = data
			break
		}
	}
	if compose == nil {
		return nil
	}
	var doc struct {
		Services map[string]struct {
			Build any `yaml:"build"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(compose, &doc) != nil {
		return nil
	}
	type context struct{ dir, dockerfile string }
	var contexts []context
	for _, service := range doc.Services {
		dir, dockerfile := "", "Dockerfile"
		switch build := service.Build.(type) {
		case string:
			dir = build
		case map[string]any:
			dir, _ = build["context"].(string)
			if value, ok := build["dockerfile"].(string); ok && value != "" {
				dockerfile = value
			}
		default:
			continue
		}
		dir = path.Clean(strings.TrimSpace(dir))
		// The root context is the root .dockerignore's job; a URL, an
		// absolute path, an interpolated or an outside context is not in
		// the archive.
		if dir == "." || dir == "" || path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") || strings.ContainsAny(dir, "$:") {
			continue
		}
		if !slices.ContainsFunc(contexts, func(c context) bool { return c.dir == dir }) {
			contexts = append(contexts, context{dir: dir, dockerfile: path.Clean(dockerfile)})
		}
	}
	slices.SortFunc(contexts, func(a, b context) int { return strings.Compare(a.dir, b.dir) })
	var out []gregaleignorePattern
	for _, ctx := range contexts {
		data, ok := readFile(ctx.dir + "/" + dockerignoreFile)
		if !ok {
			continue
		}
		prefix := strings.Split(ctx.dir, "/")
		for _, pattern := range parseDockerignore(data) {
			pattern.globSegments = append(append([]string{}, prefix...), pattern.globSegments...)
			pattern.source = ctx.dir + "/" + dockerignoreFile
			out = append(out, pattern)
		}
		keep := []string{ctx.dir + "/" + dockerignoreFile}
		if dockerfile := path.Join(ctx.dir, ctx.dockerfile); dockerfile != ctx.dir && strings.HasPrefix(dockerfile, ctx.dir+"/") {
			for dir := path.Dir(dockerfile); dir != ctx.dir && strings.HasPrefix(dir, ctx.dir+"/"); dir = path.Dir(dir) {
				keep = append(keep, dir)
			}
			keep = append(keep, dockerfile)
		}
		for _, rel := range keep {
			out = append(out, gregaleignorePattern{raw: "!" + rel, anchor: true, negate: true,
				globSegments: strings.Split(escapeGregaleignoreLiteral(rel), "/"), source: "Compose build context"})
		}
	}
	return out
}

// escapeGregaleignoreLiteral escapes glob metacharacters so a path matches
// only itself.
func escapeGregaleignoreLiteral(rel string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)
	return replacer.Replace(rel)
}
