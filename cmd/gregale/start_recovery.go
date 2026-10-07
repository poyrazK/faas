package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (r *startRunner) sourcePreflight() (doctorReport, bool, error) {
	for {
		info, err := os.Stat(r.session.SourcePath)
		if err != nil || !info.IsDir() || detectShape(r.session.SourcePath) == shapeUnknown {
			PrintWarn(osStdout, "I couldn't detect an app or function in %s.", r.session.SourcePath)
			choice, err := r.prompt.choose(r.ctx, "Choose a directory containing your app's language files or Dockerfile.", []string{"Choose another directory", "I've fixed the directory — recheck", "Finish"}, 0)
			if err != nil {
				return doctorReport{}, false, err
			}
			if choice == 2 {
				return doctorReport{}, false, nil
			}
			if choice == 0 {
				if err := r.changeSourceDirectory(); err != nil {
					return doctorReport{}, false, err
				}
			}
			continue
		}
		report := runDoctorChecksForShape(r.session.SourcePath, detectShape(r.session.SourcePath))
		if r.secretsFile != "" {
			pairs, err := readSecretsFile(r.secretsFile)
			if err != nil {
				PrintWarn(osStdout, "The supplied secrets file could not be validated: %v", err)
				choice, err := r.prompt.choose(r.ctx, "Secrets file needs attention.", []string{"Choose a secrets file", "I've fixed the file — recheck", "Finish"}, 0)
				if err != nil || choice == 2 {
					return report, false, err
				}
				if choice == 0 {
					if err := r.chooseSecretsFile(); err != nil {
						return report, false, err
					}
				}
				continue
			}
			keys := make(map[string]bool, len(pairs))
			for _, pair := range pairs {
				keys[pair.Key] = true
			}
			for i := range report.Checks {
				check := &report.Checks[i]
				if check.Name != "env-required" {
					continue
				}
				var missing []string
				for _, key := range check.Sources {
					if !keys[key] {
						missing = append(missing, key)
					}
				}
				check.Sources = missing
				if len(missing) == 0 {
					*check = doctorCheck{Name: "env-required", Status: "ok"}
				} else {
					// The doctor's decorated prose may refer to the first key
					// that was just supplied. Name only the still-missing keys.
					check.Hint = "Supply these environment keys: " + strings.Join(missing, ", ")
					check.Why = "These keys have not been supplied by the selected secrets file."
					check.Fix = "Add the remaining keys to the file, or choose another secrets file, then recheck."
				}
			}
		}
		if !report.HasErrors() {
			PrintOK(osStdout, "Source checks passed")
			for _, check := range report.Checks {
				if check.Status == "warn" && check.Hint != "" {
					PrintWarn(osStdout, "%s", check.Hint)
				}
			}
			return report, true, nil
		}
		renderDoctorHuman(osStdout, report)
		edits := startLoopbackEdits(r.session.SourcePath, report)
		choices := []string{"I've fixed the source — recheck", "Choose another directory", "Finish"}
		fixIndex, secretsIndex := -1, -1
		if len(edits) != 0 {
			fixIndex = len(choices)
			choices = append(choices, "Review a fix for the loopback listener")
		}
		for _, check := range report.Checks {
			if check.Name == "env-required" && check.Status == "error" {
				secretsIndex = len(choices)
				choices = append(choices, "Supply missing keys from a secrets file")
				break
			}
		}
		fallback := 0
		if fixIndex >= 0 {
			fallback = fixIndex
		} else if secretsIndex >= 0 {
			fallback = secretsIndex
		}
		choice, err := r.prompt.choose(r.ctx, "Let's resolve the source blockers before deploying.", choices, fallback)
		if err != nil {
			return report, false, err
		}
		switch choice {
		case 2:
			return report, false, nil
		case 1:
			if err := r.changeSourceDirectory(); err != nil {
				return report, false, err
			}
		case fixIndex:
			if _, err := r.reviewFileEdits(edits); err != nil {
				if r.ctx.Err() != nil {
					return report, false, err
				}
				PrintWarn(osStdout, "The edit was not completed: %v", err)
			}
		case secretsIndex:
			if err := r.chooseSecretsFile(); err != nil {
				return report, false, err
			}
		}
	}
}

func (r *startRunner) changeSourceDirectory() error {
	abs, err := r.requiredLocalPath("Project directory")
	if err != nil {
		return fmt.Errorf("resolve source directory: %w", err)
	}
	r.session.SourcePath, r.session.Template = abs, ""
	r.session.HealthPath = ""
	// Choosing a new project must not carry a secrets file from another one.
	r.secretsFile = ""
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		selected, err := r.selectProjectSource(abs)
		if err != nil {
			return err
		}
		r.session.SourcePath = selected
	}
	return nil
}

func (r *startRunner) chooseSecretsFile() error {
	_, _ = fmt.Fprintln(osStdout, "Use a local KEY=VALUE file. Values will be sealed during the reviewed deployment and omitted from session metadata.")
	abs, err := r.requiredLocalPath("Secrets file path")
	if err != nil {
		return fmt.Errorf("resolve secrets file: %w", err)
	}
	r.secretsFile = abs
	return nil
}

func (r *startRunner) requiredLocalPath(label string) (string, error) {
	for {
		path, err := r.prompt.text(r.ctx, label, "")
		if err != nil {
			return "", err
		}
		if path == "" {
			PrintWarn(osStdout, "Enter a path to continue.")
			continue
		}
		return filepath.Abs(path)
	}
}
