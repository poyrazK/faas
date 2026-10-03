package reposcan

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// procfileLines is a Heroku-format Procfile. Each non-blank,
// non-comment line carries one process-type mapping:
//
//	web:      bundle exec rails s
//	worker:   bundle exec sidekiq
//	cron:     bundle exec nightly
//	clock:    bundle exec scheduler   ← also a job (clock is Heroku-Schduler-speak)
//	scheduler: bundle exec scheduler   ← also a job
//	release:  bundle exec rake deploy  ← pre-activation release command
//
// We split each line at the FIRST colon (RHS may itself contain
// colons); anything after the colon is the start command.
func detectProcfile(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	body, src, err := readFirstValidFile(fsys, []string{nameProcfile})
	if err != nil || body == nil {
		return nil, nil, nil, err
	}
	var seeds []workloadSeed
	var warnings []string
	for _, line := range splitLines(string(body)) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		procName := strings.TrimSpace(line[:i])
		command := strings.TrimSpace(line[i+1:])
		if procName == "" || command == "" {
			continue
		}
		if procName == keyRelease {
			continue
		}
		class, include := procfileClass(procName)
		if !include {
			continue // build hooks (release) skipped silently
		}
		// Some Procfile entries name the workload after the type
		// (e.g. "web:") rather than the service name. We use the
		// process name itself as the workload name so the merge
		// rule can pair it with a compose `web` service (same key
		// = same (RootDir="", Name="web")).
		seeds = append(seeds, workloadSeed{
			name:         procName,
			rootDir:      "",
			command:      []string{command},
			commandShell: true,
			class:        class,
			source:       src + ": " + procName,
		})
	}
	// Deterministic order to keep merge input stable.
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	if len(seeds) == 0 && len(warnings) == 0 {
		// file present but no usable lines (comments only?)
		warnings = append(warnings, "reposcan: "+src+
			": no usable process-type lines (comments only?)")
	}
	return seeds, nil, warnings, nil
}

// ParseProcfileReleaseCommand returns the single release process from a
// Procfile without manufacturing a workload. Source-deploy handlers use this
// when they already know the target app from the request.
func ParseProcfileReleaseCommand(body []byte) (string, bool, error) {
	var command string
	for _, line := range splitLines(string(body)) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 || strings.TrimSpace(line[:i]) != keyRelease {
			continue
		}
		candidate := strings.TrimSpace(line[i+1:])
		if candidate == "" {
			continue
		}
		if command != "" {
			return "", false, fmt.Errorf("duplicate release process")
		}
		if err := validateProcfileReleaseCommand(candidate); err != nil {
			return "", false, err
		}
		command = candidate
	}
	return command, command != "", nil
}

func validateProcfileReleaseCommand(command string) error {
	if strings.ContainsRune(command, '\x00') {
		return fmt.Errorf("command contains NUL")
	}
	// Release commands are shell form and therefore one app-task argument.
	// Keep this bound aligned with api.AppTaskMaxCommandArgBytes and the
	// app_tasks_command_chk database constraint.
	if len(command) > 4096 {
		return fmt.Errorf("command exceeds 4096 bytes")
	}
	return nil
}

// procfileClass maps the process-type to a workload class. Returns
// (class, include). `include=false` is the skip path (release:
// is a build hook, not a workload).
func procfileClass(procName string) (Class, bool) {
	switch procName {
	case keyWeb:
		return ClassHTTP, true
	case keyWorker, keyConsumer:
		return ClassWorker, true
	case keyCron, keyClock, keyScheduler:
		return ClassJob, true
	case keyRelease:
		return "", false
	}
	// Unknown process type is still a workload — class is left
	// unknown so the Phase 4 boot can characterize it.
	return ClassUnknown, true
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}
