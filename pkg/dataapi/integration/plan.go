// Package integration adds an isolated Data API example to an existing npm app.
package integration

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/*
var assets embed.FS

const Config = "gregale-data/data-api.dev.json"
const Requests = "gregale-data/requests.json"
const ClientSource = "src/gregale-data"

type File struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Content string `json:"content"`
	before  []byte
	existed bool
	mode    os.FileMode
}

type Plan struct {
	Directory       string `json:"directory"`
	ClientDirectory string `json:"client_directory"`
	Files           []File `json:"files"`
	GeneratedTypes  string `json:"generated_types"`
	reserved        []string
}

// Build is offline and makes no changes. Existing app sources and scripts are retained.
func Build(directory, client string) (*Plan, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if !filepath.IsLocal(client) {
		return nil, errors.New("client directory must be project-relative")
	}
	client = filepath.Clean(client)
	if err := safePath(root, filepath.Join(client, "package.json")); err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(root, client, "package.json"))
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("client package.json must be a regular file under 1 MiB")
	}
	metadata, err := os.ReadFile(filepath.Join(root, client, "package.json"))
	if err != nil {
		return nil, errors.New("client package.json must exist")
	}
	var pkg struct {
		PackageManager  string                     `json:"packageManager"`
		Workspaces      json.RawMessage            `json:"workspaces"`
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if len(metadata) > 1<<20 || json.Unmarshal(metadata, &pkg) != nil {
		return nil, errors.New("client package.json must be a JSON object under 1 MiB")
	}
	if pkg.PackageManager != "" && !strings.HasPrefix(pkg.PackageManager, "npm@") || len(pkg.Workspaces) != 0 {
		return nil, errors.New("integration currently supports standalone npm packages, not workspaces")
	}
	if pkg.Dependencies["typescript"] == nil && pkg.DevDependencies["typescript"] == nil {
		return nil, errors.New("declare TypeScript 5 or newer in the client package before integrating")
	}
	for current := filepath.Join(root, client); ; current = filepath.Dir(current) {
		for _, lock := range []string{"yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"} {
			if _, err := os.Lstat(filepath.Join(current, lock)); !errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("integration currently supports npm lockfiles only")
			}
		}
		if current == root {
			break
		}
		var parent struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		data, readErr := os.ReadFile(filepath.Join(filepath.Dir(current), "package.json"))
		if readErr == nil && json.Unmarshal(data, &parent) == nil && len(parent.Workspaces) != 0 {
			return nil, errors.New("integration currently supports standalone npm packages, not workspaces")
		}
	}
	p := &Plan{Directory: root, ClientDirectory: filepath.ToSlash(client), GeneratedTypes: filepath.ToSlash(filepath.Join(client, ClientSource, "database.types.ts"))}
	p.reserved = []string{"gregale-data", filepath.Join(client, ClientSource), "data-api-artifacts.json", filepath.Join(client, "vendor", "gregale-data.tgz")}
	for _, name := range p.reserved {
		if err := safePath(root, name); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("integration destination already exists: %s; use the printed install/serve commands to continue a partial integration", name)
		}
	}
	for _, name := range []string{"package-lock.json", "node_modules", ".gregale", ".gitignore"} {
		if err := safePath(root, name); err != nil {
			return nil, err
		}
		if err := safePath(root, filepath.Join(client, name)); err != nil {
			return nil, err
		}
	}
	for source, target := range map[string]string{
		"client.ts":      filepath.Join(client, ClientSource, "client.ts"),
		"tsconfig.json":  filepath.Join(client, ClientSource, "tsconfig.json"),
		"0001_notes.sql": "gregale-data/migrations/0001_notes.sql",
		"requests.json":  Requests,
		"README.md":      "gregale-data/README.md",
	} {
		content, err := assets.ReadFile("assets/" + source)
		if err != nil {
			return nil, err
		}
		p.Files = append(p.Files, File{Path: filepath.ToSlash(target), Action: "create", Content: string(content), mode: 0o644})
	}
	config := map[string]any{
		"version": 1, "migrations": "gregale-data/migrations", "output": p.GeneratedTypes,
		"identities": map[string]any{"alice": map[string]string{"subject": "alice"}, "bob": map[string]string{"subject": "bob"}},
		"verify":     map[string]any{"collection": Requests, "scenarios": []string{"notes-rls"}},
		"check":      map[string]any{"directory": p.ClientDirectory, "commands": [][]string{{"npm", "exec", "--offline", "--", "tsc", "--project", ClientSource + "/tsconfig.json"}}},
	}
	encoded, _ := json.MarshalIndent(config, "", "  ")
	p.Files = append(p.Files, File{Path: Config, Action: "create", Content: string(encoded) + "\n", mode: 0o644})
	ignore := File{Path: ".gitignore", Action: "create", mode: 0o644}
	if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		ignore.before, ignore.existed, ignore.Action = data, true, "append"
		info, err := os.Stat(filepath.Join(root, ".gitignore"))
		if err != nil {
			return nil, err
		}
		ignore.mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	rule := "/.gregale/data-api-dev-*.json"
	if !strings.Contains("\n"+string(ignore.before)+"\n", "\n"+rule+"\n") {
		ignore.Content = string(ignore.before)
		if ignore.Content != "" && !strings.HasSuffix(ignore.Content, "\n") {
			ignore.Content += "\n"
		}
		ignore.Content += rule + "\n"
		p.Files = append(p.Files, ignore)
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	return p, nil
}

func safePath(root, name string) error {
	if !filepath.IsLocal(name) {
		return errors.New("integration paths must stay inside the project")
	}
	current := root
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("integration paths cannot contain symlinks: %s", name)
		}
	}
	return nil
}

// Apply refuses stale plans and never overwrites an application source file.
func (p *Plan) Apply() error {
	r, err := os.OpenRoot(p.Directory)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	for _, name := range p.reserved {
		if err := safePath(p.Directory, name); err != nil {
			return err
		}
		if _, err := r.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("project changed after planning: %s", name)
		}
	}
	for _, f := range p.Files {
		if err := safePath(p.Directory, f.Path); err != nil {
			return err
		}
		current, err := r.ReadFile(f.Path)
		if f.existed && (err != nil || !bytes.Equal(current, f.before)) || !f.existed && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("project changed after planning: %s", f.Path)
		}
	}
	var created []string
	rollback := func() {
		for _, name := range created {
			_ = r.Remove(name)
		}
	}
	for _, f := range p.Files {
		if f.existed {
			continue
		}
		if err := r.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			rollback()
			return err
		}
		file, err := r.OpenFile(f.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.mode)
		if err != nil {
			rollback()
			return err
		}
		created = append(created, f.Path)
		_, err = file.WriteString(f.Content)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			rollback()
			return errors.Join(err, closeErr)
		}
	}
	for _, f := range p.Files {
		if !f.existed {
			continue
		}
		// There is only one edited input: append the session-file ignore rule.
		file, err := r.OpenFile(f.Path, os.O_WRONLY|os.O_APPEND, f.mode)
		if err != nil {
			rollback()
			return err
		}
		_, err = file.WriteString(f.Content[len(f.before):])
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			rollback()
			return errors.Join(err, closeErr)
		}
	}
	return nil
}
