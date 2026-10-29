package main

// adr: 20261024085817
//
//	vmmd base-generation-check   run with the CANDIDATE binary during a
//	                             compute rollout, while the serving vmmd
//	                             still runs. Compares the runtime base
//	                             generations the serving vmmd boots (its
//	                             process environment) with the candidate's
//	                             runtime-bases.env, and reads this node's
//	                             cache generation markers. Exit 0: no
//	                             generation changes and the serving bases
//	                             are intact; 3: the candidate changes a
//	                             generation and the serving bases are still
//	                             intact (drain before pre-staging); 4: the
//	                             cache already holds another generation of a
//	                             base the serving vmmd boots, which refuses
//	                             every guest on it (never return the node to
//	                             service on this release); 1: the check
//	                             itself failed.
//
// It prints one JSON object on stdout.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	baseGenerationExitIntact    = 0
	baseGenerationExitChanged   = 3
	baseGenerationExitAbandoned = 4
)

// Per-base verdicts. "current": the cache marker names the serving
// generation. "other_generation": it names another, so the serving vmmd's
// ensureBaseGeneration refreshes from shared storage and refuses the boot
// when the shared copy is not its generation. "not_cached": no marker; the
// serving vmmd fetches the base at its first boot here, as before.
// "uncached_storage": the route has no read-through cache (single-box local
// storage), which vmmd does not gate.
const (
	baseCacheCurrent         = "current"
	baseCacheOtherGeneration = "other_generation"
	baseCacheNotCached       = "not_cached"
	baseCacheUncachedStorage = "uncached_storage"
)

type baseGenerationReport struct {
	// Changed: the candidate configures another generation for at least
	// one base.
	Changed bool `json:"changed"`
	// ServingIntact: no base the serving vmmd boots has another generation
	// committed in this node's cache.
	ServingIntact bool                  `json:"serving_intact"`
	Bases         []baseGenerationEntry `json:"bases"`
}

type baseGenerationEntry struct {
	Key       string `json:"key"`
	Serving   string `json:"serving"`
	Candidate string `json:"candidate"`
	Cached    string `json:"cached,omitempty"`
	Cache     string `json:"cache"`
}

func (r baseGenerationReport) exitCode() int {
	switch {
	case !r.ServingIntact:
		return baseGenerationExitAbandoned
	case r.Changed:
		return baseGenerationExitChanged
	}
	return baseGenerationExitIntact
}

// cachedGeneration reads one base's cache marker: ok=false with routed=true
// means no marker, routed=false means the key has no read-through cache.
type cachedGeneration func(key string) (generation string, ok, routed bool, err error)

// judgeBaseGenerations compares the serving and candidate generations of
// every runtime base and reads each one's cache marker. It mirrors the
// serving vmmd's ensureBaseGeneration without refreshing anything.
func judgeBaseGenerations(serving, candidate map[string]string, cached cachedGeneration) (baseGenerationReport, error) {
	keys := make([]string, 0, len(serving)+len(candidate))
	seen := make(map[string]bool, len(serving)+len(candidate))
	for _, m := range []map[string]string{serving, candidate} {
		for key := range m {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	report := baseGenerationReport{ServingIntact: true, Bases: make([]baseGenerationEntry, 0, len(keys))}
	for _, key := range keys {
		entry := baseGenerationEntry{Key: key, Serving: serving[key], Candidate: candidate[key]}
		if entry.Serving != entry.Candidate {
			report.Changed = true
		}
		generation, ok, routed, err := cached(key)
		if err != nil {
			return baseGenerationReport{}, fmt.Errorf("read cache generation of %s: %w", key, err)
		}
		switch {
		case !routed:
			entry.Cache = baseCacheUncachedStorage
		case !ok:
			entry.Cache = baseCacheNotCached
		case generation == entry.Serving:
			entry.Cache, entry.Cached = baseCacheCurrent, generation
		default:
			entry.Cache, entry.Cached = baseCacheOtherGeneration, generation
			// vmmd gates only a base it has a configured generation for.
			if entry.Serving != "" {
				report.ServingIntact = false
			}
		}
		report.Bases = append(report.Bases, entry)
	}
	return report, nil
}

// baseGenerationStorage opens the storage backend the serving vmmd uses,
// from its environment. Tests replace it.
var baseGenerationStorage = func(ctx context.Context, env map[string]string) (storage.StorageBackend, error) {
	// pkg/storage reads the process environment. This one-shot command owns
	// its environment, so replace it with the serving vmmd's.
	os.Clearenv()
	for key, value := range env {
		if err := os.Setenv(key, value); err != nil {
			return nil, err
		}
	}
	return storage.BackendFromEnvContext(ctx)
}

func baseGenerationCheckCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("base-generation-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	servingPID := fs.Int("serving-pid", 0, "the serving vmmd's process id; its environment is the serving configuration")
	servingEnviron := fs.String("serving-environ", "", "a NUL-separated environment file to read instead of /proc/<serving-pid>/environ")
	candidateEnvFile := fs.String("candidate-env-file", "/etc/faas/runtime-bases.env", "the candidate release's runtime-bases.env")
	timeout := fs.Duration("timeout", time.Minute, "how long opening the storage backend may take")
	if err := fs.Parse(args); err != nil {
		return adoptionExitError
	}
	report, err := runBaseGenerationCheck(*servingPID, *servingEnviron, *candidateEnvFile, *timeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vmmd base-generation-check: %v\n", err)
		return adoptionExitError
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return adoptionExitError
	}
	return report.exitCode()
}

func runBaseGenerationCheck(pid int, environPath, candidateEnvFile string, timeout time.Duration) (baseGenerationReport, error) {
	servingEnv, err := readServingEnviron(pid, environPath)
	if err != nil {
		return baseGenerationReport{}, err
	}
	candidateFile, err := os.ReadFile(candidateEnvFile) //nolint:forbidigo // operator-supplied release env file
	if err != nil {
		return baseGenerationReport{}, fmt.Errorf("read candidate runtime bases: %w", err)
	}
	candidateEnv := make(map[string]string, len(servingEnv))
	for key, value := range servingEnv {
		candidateEnv[key] = value
	}
	for key, value := range parseEnvFile(candidateFile) {
		candidateEnv[key] = value
	}
	serving, err := configuredRuntimeBaseGenerations(mapLookup(servingEnv))
	if err != nil {
		return baseGenerationReport{}, fmt.Errorf("serving generations: %w", err)
	}
	candidate, err := configuredRuntimeBaseGenerations(mapLookup(candidateEnv))
	if err != nil {
		return baseGenerationReport{}, fmt.Errorf("candidate generations: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	be, err := baseGenerationStorage(ctx, servingEnv)
	if err != nil {
		return baseGenerationReport{}, fmt.Errorf("open the serving storage backend: %w", err)
	}
	return judgeBaseGenerations(serving, candidate, func(key string) (string, bool, bool, error) {
		cache, cacheKey, err := storage.CacheBackendForKey(be, key)
		if err != nil || cache == nil {
			return "", false, false, err
		}
		generation, ok, err := cache.CachedGeneration(cacheKey)
		return generation, ok, true, err
	})
}

// readServingEnviron reads a vmmd process's environment: environPath when
// set, else /proc/<pid>/environ.
func readServingEnviron(pid int, environPath string) (map[string]string, error) {
	if environPath == "" {
		if pid <= 0 {
			return nil, fmt.Errorf("--serving-pid or --serving-environ is required")
		}
		environPath = filepath.Join("/proc", strconv.Itoa(pid), "environ")
	}
	raw, err := os.ReadFile(environPath) //nolint:forbidigo // operator-supplied process environment
	if err != nil {
		return nil, fmt.Errorf("read serving environment: %w", err)
	}
	return parseEnviron(raw), nil
}

func mapLookup(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

// parseEnviron reads /proc/<pid>/environ: NUL-separated KEY=VALUE entries.
func parseEnviron(raw []byte) map[string]string {
	env := make(map[string]string)
	for _, entry := range bytes.Split(raw, []byte{0}) {
		key, value, ok := strings.Cut(string(entry), "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	return env
}

// parseEnvFile reads a systemd EnvironmentFile of KEY=VALUE lines. Blank
// lines and comments are skipped and one pair of surrounding quotes is
// removed, which covers runtime-bases.env.
func parseEnvFile(raw []byte) map[string]string {
	env := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		env[key] = value
	}
	return env
}
