package main

// `gregale add function NAME` scaffolds a function as one directory whose
// handler declares its own config (ADR-20261107193000), so `gregale deploy`
// discovers it with no gregale.yaml edit.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

var (
	addFunctionTriggers   = []string{"http", "cron", "queue", "bucket", "webhook"}
	addFunctionLangs      = []string{"node", "python"}
	addFunctionProviders  = []string{"stripe", "generic"}
	addFunctionResourceRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

const addFunctionUsage = "usage: gregale add function NAME [--trigger http|cron|queue|bucket|webhook] [--lang node|python] [--schedule CRON] [--queue NAME] [--bucket NAME] [--provider stripe|generic] [--path DIR] [--dry-run]"

// addFunctionOptions are the parsed flags of one scaffold.
type addFunctionOptions struct {
	Name, Trigger, Lang, Schedule, Queue, Bucket, Provider, ProjectDir string
	DryRun                                                             bool
}

// addFunctionResult is the --json shape.
type addFunctionResult struct {
	Function   string   `json:"function"`
	Trigger    string   `json:"trigger"`
	Lang       string   `json:"lang"`
	Files      []string `json:"files"`
	Written    bool     `json:"written"`
	Discovered bool     `json:"discovered"`
	// ManifestSnippet is the functions: entry to add when gregale.yaml
	// lists functions or configures a single app, so discovery is off.
	ManifestSnippet string `json:"manifest_snippet,omitempty"`
	Note            string `json:"note,omitempty"`
}

type scaffoldFile struct {
	Rel  string
	Body string
}

func cmdAddFunction(args []string) int {
	opts, ok := parseAddFunctionFlags(args)
	if !ok {
		return 1
	}
	files, err := scaffoldFunction(opts)
	if err != nil {
		return printErr("Invalid function", err)
	}
	result := addFunctionResult{Function: opts.Name, Trigger: opts.Trigger, Lang: opts.Lang}
	for _, f := range files {
		result.Files = append(result.Files, f.Rel)
	}
	result.Discovered, result.ManifestSnippet, result.Note, err = addFunctionDiscovery(opts)
	if err != nil {
		return printErr("Could not read gregale.yaml", err)
	}
	if !opts.DryRun {
		if err := writeScaffold(opts.ProjectDir, files); err != nil {
			return printErr("Could not create function", err)
		}
		result.Written = true
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	printAddFunctionResult(result)
	return 0
}

func parseAddFunctionFlags(args []string) (addFunctionOptions, bool) {
	fs := newFlagSet("add function", flag.ContinueOnError)
	opts := addFunctionOptions{}
	fs.StringVar(&opts.Trigger, "trigger", "http", "what calls the function: "+strings.Join(addFunctionTriggers, "|"))
	fs.StringVar(&opts.Lang, "lang", "", "node|python (default: inferred from the project, else node)")
	fs.StringVar(&opts.Schedule, "schedule", "0 * * * *", "cron schedule for --trigger cron")
	fs.StringVar(&opts.Queue, "queue", "", "queue name for --trigger queue (default: the function name)")
	fs.StringVar(&opts.Bucket, "bucket", "", "bucket name for --trigger bucket (required)")
	fs.StringVar(&opts.Provider, "provider", "generic", "webhook provider for --trigger webhook: stripe|generic")
	fs.StringVar(&opts.ProjectDir, "path", ".", "project directory")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "show the files without writing them")
	if err := parseInterspersed(fs, args); err != nil {
		return opts, false
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, addFunctionUsage, "add")
		return opts, false
	}
	opts.Name = fs.Arg(0)
	if opts.Lang == "" {
		opts.Lang = inferFunctionLang(opts.ProjectDir)
	}
	if opts.Queue == "" {
		opts.Queue = opts.Name
	}
	if err := opts.validate(); err != nil {
		PrintFail(os.Stderr, "%v", err)
		PrintUsage(os.Stderr, addFunctionUsage, "add")
		return opts, false
	}
	return opts, true
}

func (o addFunctionOptions) validate() error {
	switch {
	case !gregalemanifest.ValidFunctionName(o.Name):
		return fmt.Errorf("function name %q must be 1-30 lowercase letters, digits or hyphens, starting with a letter", o.Name)
	case !containsString(addFunctionTriggers, o.Trigger):
		return fmt.Errorf("--trigger must be one of %s", strings.Join(addFunctionTriggers, ", "))
	case !containsString(addFunctionLangs, o.Lang):
		return fmt.Errorf("--lang must be node or python")
	case o.Trigger == "queue" && !addFunctionResourceRE.MatchString(o.Queue):
		return fmt.Errorf("--queue %q must be lowercase letters, digits and hyphens, starting with a letter", o.Queue)
	case o.Trigger == "bucket" && !addFunctionResourceRE.MatchString(o.Bucket):
		return errors.New("--trigger bucket needs --bucket NAME (lowercase letters, digits and hyphens)")
	case o.Trigger == "webhook" && !containsString(addFunctionProviders, o.Provider):
		return errors.New("--provider must be stripe or generic")
	}
	return nil
}

// inferFunctionLang picks python for a Python-only project, else node.
func inferFunctionLang(dir string) string {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if !exists("package.json") && (exists("pyproject.toml") || exists("requirements.txt")) {
		return "python"
	}
	return "node"
}

// scaffoldFunction renders the files and checks the handler's config with
// the parser and validation `gregale deploy` uses.
func scaffoldFunction(o addFunctionOptions) ([]scaffoldFile, error) {
	root := path.Join(gregalemanifest.FunctionsDiscoveryDir, o.Name)
	if _, err := os.Stat(filepath.Join(o.ProjectDir, filepath.FromSlash(root))); err == nil {
		return nil, fmt.Errorf("%s already exists; choose another name or remove it", root)
	}
	files := renderFunctionScaffold(o, root)
	cfg, found, err := gregalemanifest.ParseInlineConfig(files[0].Rel, []byte(files[0].Body))
	if err != nil {
		return nil, fmt.Errorf("generated config does not parse (please report this): %w", err)
	}
	if !found {
		return nil, errors.New("generated handler declares no config (please report this)")
	}
	cfg.Spec.Name, cfg.Spec.Root = o.Name, root
	m := &gregalemanifest.Manifest{Functions: []gregalemanifest.FunctionSpec{cfg.Spec}, Triggers: gregalemanifest.CronTriggers(o.Name, cfg.Crons)}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return files, nil
}

// addFunctionDiscovery reports whether deploy will discover the function;
// otherwise it returns the functions: entry to add and, for a single-app
// manifest, why that entry alone is not enough.
func addFunctionDiscovery(o addFunctionOptions) (bool, string, string, error) {
	m, found, err := gregalemanifest.Load(o.ProjectDir)
	if err != nil {
		return false, "", "", err
	}
	if !found || gregalemanifest.AllowsFunctionDiscovery(m) {
		return true, "", "", nil
	}
	snippet := fmt.Sprintf("functions:\n  - name: %s\n    root: %s/%s\n", o.Name, gregalemanifest.FunctionsDiscoveryDir, o.Name)
	if m.Functions == nil {
		return false, snippet, "gregale.yaml configures a single app, so functions/ directories are not discovered; a project deploys its functions once gregale.yaml holds only a functions: list (docs/functions.md)", nil
	}
	return false, snippet, "", nil
}

func writeScaffold(projectDir string, files []scaffoldFile) error {
	for _, f := range files {
		abs := filepath.Join(projectDir, filepath.FromSlash(f.Rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		fh, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, werr := fh.WriteString(f.Body)
		if cerr := fh.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	return nil
}

func printAddFunctionResult(r addFunctionResult) {
	verb := "Created"
	if !r.Written {
		verb = "Would create"
	}
	PrintOK(osStdout, "%s function %s (%s, %s trigger):", verb, r.Function, r.Lang, r.Trigger)
	for _, f := range r.Files {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", f)
	}
	switch {
	case r.Note != "":
		_, _ = fmt.Fprintf(osStdout, "\n%s.\n", r.Note)
	case r.ManifestSnippet != "":
		_, _ = fmt.Fprintf(osStdout, "\ngregale.yaml lists its functions, so add this entry:\n\n%s", r.ManifestSnippet)
	}
	test := "node --test " + r.Files[1]
	if r.Lang == "python" {
		test = "python -m pytest " + r.Files[1]
	}
	_, _ = fmt.Fprintf(osStdout, "\nNext:\n  %s\n  gregale deploy --plan\n  gregale deploy\n", test)
}
