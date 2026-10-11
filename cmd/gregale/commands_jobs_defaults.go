package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type localJobDefaults struct {
	Version   int      `json:"version"`
	Job       string   `json:"job"`
	Tasks     int      `json:"tasks,omitempty"`
	Results   bool     `json:"results"`
	WaitReady bool     `json:"wait_ready,omitempty"`
	Include   []string `json:"include,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
}

func (d localJobDefaults) validate() error {
	if d.Version != 1 || !jobSlugPattern.MatchString(d.Job) || d.Tasks < 0 {
		return errors.New("Job defaults require version 1, a valid Job name, and a nonnegative task count")
	}
	if err := validateJobArtifactIncludes(d.Include); err != nil {
		return err
	}
	if err := validateJobArtifactExcludes(d.Exclude); err != nil {
		return err
	}
	return nil
}

func jobDefaultsPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, projectContextDirName, "jobs.json"), nil
}

func readJobDefaults() (localJobDefaults, string, error) {
	path, err := jobDefaultsPath()
	if err != nil {
		return localJobDefaults{}, path, err
	}
	file, err := os.Open(path)
	if err != nil {
		return localJobDefaults{}, path, err
	}
	defer file.Close()
	var d localJobDefaults
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, path, fmt.Errorf("parse %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return d, path, fmt.Errorf("%s must contain a single JSON object", path)
	}
	if err := d.validate(); err != nil {
		return d, path, fmt.Errorf("validate %s: %w", path, err)
	}
	return d, path, nil
}

func cmdJobsDefaults(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid defaults command", errors.New("use jobs defaults set JOB [--tasks N] [--results], update [JOB] [options], show, or clear"))
	}
	switch args[0] {
	case "set", "update":
		updating := args[0] == "update"
		fs := newFlagSet("jobs defaults "+args[0], flag.ContinueOnError)
		tasks := fs.Int("tasks", 0, "default task count (positive when provided)")
		results := fs.Bool("results", false, "collect results by default")
		waitReady := fs.Bool("wait-ready", false, "wait for image preparation before submitting by default")
		includes := registerJobsMultiFlag(fs, "include", "repeatable saved artifact-name glob; quote patterns")
		excludes := registerJobsMultiFlag(fs, "exclude", "repeatable saved artifact-name glob to omit")
		var allArtifacts *bool
		if updating {
			allArtifacts = fs.Bool("all-artifacts", false, "clear saved artifact filters")
		}
		if err := parseInterspersed(fs, args[1:]); err != nil {
			return 1
		}
		provided := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
		if (!updating && fs.NArg() != 1) || (updating && fs.NArg() > 1) || (provided["tasks"] && *tasks <= 0) {
			return printErr("Invalid Job defaults", errors.New("set requires a Job name; update accepts an optional Job name; --tasks must be positive"))
		}
		d := localJobDefaults{Version: 1, Job: fs.Arg(0), Tasks: *tasks, Results: *results, WaitReady: *waitReady, Include: []string(*includes), Exclude: []string(*excludes)}
		if updating {
			if err := validateAllJobArtifacts(fs, *allArtifacts); err != nil {
				return printErr("Invalid artifact filter", err)
			}
			if fs.NArg() == 0 && !provided["tasks"] && !provided["results"] && !provided["wait-ready"] && !provided["include"] && !provided["exclude"] && !*allArtifacts {
				return printErr("No defaults changes", errors.New("provide a Job name or --tasks, --results, --wait-ready, --include, --exclude, or --all-artifacts"))
			}
			existing, _, err := readJobDefaults()
			if err != nil {
				return printErr("Could not read Job defaults", err)
			}
			d = existing
			if fs.NArg() == 1 {
				d.Job = fs.Arg(0)
			}
			if provided["tasks"] {
				d.Tasks = *tasks
			}
			if provided["results"] {
				d.Results = *results
			}
			if provided["wait-ready"] {
				d.WaitReady = *waitReady
			}
			if provided["include"] || provided["exclude"] {
				d.Include = []string(*includes)
				d.Exclude = []string(*excludes)
			}
			if *allArtifacts {
				d.Include = nil
				d.Exclude = nil
			}
		}
		if err := d.validate(); err != nil {
			return printErr("Invalid Job defaults", err)
		}
		path, err := jobDefaultsPath()
		if err != nil {
			return printErr("Could not locate Job defaults", err)
		}
		dir := filepath.Dir(path)
		if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return printErr("Invalid defaults directory", errors.New(".gregale must be a real directory"))
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not inspect defaults directory", err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return printErr("Could not create defaults directory", err)
		}
		data, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return printErr("Could not encode Job defaults", err)
		}
		tmp, err := os.CreateTemp(dir, ".jobs-*.tmp")
		if err != nil {
			return printErr("Could not save Job defaults", err)
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if _, err = tmp.Write(append(data, '\n')); err == nil {
			err = tmp.Sync()
		}
		if err == nil {
			err = tmp.Close()
		}
		if err == nil {
			err = os.Rename(tmp.Name(), path)
		}
		if err != nil {
			return printErr("Could not save Job defaults", err)
		}
		if jsonOutput {
			return jsonOut(writeJSONSingle(struct {
				Defaults localJobDefaults `json:"defaults"`
				Path     string           `json:"path"`
			}{d, path}))
		}
		printSavedJobArtifactFilters(d)
		_, _ = fmt.Fprintf(osStdout, "Wait for image: %t\n", d.WaitReady)
		PrintOK(osStdout, "Job defaults saved: %s; tasks=%d; results=%t\n  %s", d.Job, d.Tasks, d.Results, path)
		return 0
	case "show":
		if len(args) != 1 {
			return printErr("Invalid defaults command", errors.New("jobs defaults show takes no arguments"))
		}
		d, path, err := readJobDefaults()
		if err != nil {
			return printErr("Could not read Job defaults", err)
		}
		if jsonOutput {
			return jsonOut(writeJSONSingle(struct {
				Defaults localJobDefaults `json:"defaults"`
				Path     string           `json:"path"`
			}{d, path}))
		}
		_, _ = fmt.Fprintf(osStdout, "Job: %s\nTasks: %d (0 means unset)\nResults: %t\nWait for image: %t\nPath: %s\n", d.Job, d.Tasks, d.Results, d.WaitReady, path)
		printSavedJobArtifactFilters(d)
		return 0
	case "clear":
		if len(args) != 1 {
			return printErr("Invalid defaults command", errors.New("jobs defaults clear takes no arguments"))
		}
		path, err := jobDefaultsPath()
		if err != nil {
			return printErr("Could not locate Job defaults", err)
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return printErr("Could not clear Job defaults", err)
		}
		if jsonOutput {
			return jsonOut(writeJSONSingle(struct {
				Cleared bool   `json:"cleared"`
				Path    string `json:"path"`
			}{true, path}))
		}
		PrintOK(osStdout, "Job defaults cleared: %s", path)
		return 0
	default:
		return printErr("Invalid defaults command", fmt.Errorf("unknown defaults subcommand %q", args[0]))
	}
}

// Defaults apply only to their configured Job, and only to omitted options.
func applyJobRunDefaults(fs *flag.FlagSet, name string, tasks *int, results, waitReady *bool, hasInputs bool, includes, excludes *jobsMultiFlag, allArtifacts bool) (string, error) {
	d, path, err := readJobDefaults()
	if errors.Is(err, os.ErrNotExist) {
		if name == "" {
			return "", fmt.Errorf("no saved Job is configured; provide a Job name, or save one with %s jobs defaults set my-job --tasks 1 (replace my-job with your Job name). Preview a run with %s jobs run my-job --tasks 1 --dry-run", recoveryCLI(), recoveryCLI())
		}
		return name, nil
	}
	if err != nil {
		return "", err
	}
	if name != "" && name != d.Job {
		return name, nil
	}
	used := []string{}
	if name == "" {
		name = d.Job
		used = append(used, "job="+name)
	}
	provided := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if !provided["tasks"] && !hasInputs && d.Tasks > 0 {
		*tasks = d.Tasks
		used = append(used, fmt.Sprintf("tasks=%d", d.Tasks))
	}
	if !provided["results"] && !provided["out-dir"] && d.Results {
		*results = true
		used = append(used, "results=true")
	}
	if !provided["wait-ready"] && d.WaitReady {
		*waitReady = true
		used = append(used, "wait-ready=true")
	}
	if *results || provided["out-dir"] {
		if applySavedJobArtifactFilters(fs, d, includes, excludes, allArtifacts) {
			used = append(used, "artifact filters")
		}
	}
	if len(used) > 0 && !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: %s\n", path, strings.Join(used, ", "))
	}
	return name, nil
}

func printSavedJobArtifactFilters(d localJobDefaults) {
	for _, p := range d.Include {
		_, _ = fmt.Fprintf(osStdout, "Include: %s\n", recoveryArgument(p))
	}
	for _, p := range d.Exclude {
		_, _ = fmt.Fprintf(osStdout, "Exclude: %s\n", recoveryArgument(p))
	}
}

// Any explicit filter replaces the entire saved include/exclude selection.
func applySavedJobArtifactFilters(fs *flag.FlagSet, d localJobDefaults, includes, excludes *jobsMultiFlag, allArtifacts bool) bool {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "include" || f.Name == "exclude" {
			explicit = true
		}
	})
	if explicit || allArtifacts {
		return false
	}
	*includes = append(jobsMultiFlag(nil), d.Include...)
	*excludes = append(jobsMultiFlag(nil), d.Exclude...)
	return len(d.Include) > 0 || len(d.Exclude) > 0
}

func validateAllJobArtifacts(fs *flag.FlagSet, allArtifacts bool) error {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "include" || f.Name == "exclude" {
			explicit = true
		}
	})
	if allArtifacts && explicit {
		return errors.New("--all-artifacts cannot be combined with --include or --exclude. Choose --all-artifacts to ignore or clear saved filters, or omit it and supply --include/--exclude to select artifacts")
	}
	return nil
}

func applyJobResultsDefaults(fs *flag.FlagSet, name string, includes, excludes *jobsMultiFlag, allArtifacts bool) (string, error) {
	d, path, err := readJobDefaults()
	if errors.Is(err, os.ErrNotExist) && name != "" {
		return name, nil
	}
	if err != nil {
		return "", err
	}
	if name != "" && name != d.Job {
		return name, nil
	}
	used := []string{}
	if name == "" {
		name = d.Job
		used = append(used, "job="+name)
	}
	if applySavedJobArtifactFilters(fs, d, includes, excludes, allArtifacts) {
		used = append(used, "artifact filters")
	}
	if len(used) > 0 && !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Using Job defaults from %s: %s\n", path, strings.Join(used, ", "))
	}
	return name, nil
}
