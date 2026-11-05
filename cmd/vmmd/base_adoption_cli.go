package main

// adr: 20261024191249
//
//	vmmd base-adoption-check   run with the INSTALLED binary after a compute
//	                           rollout restarted vmmd, and before the node is
//	                           returned to service. Reads the serving vmmd's
//	                           configured runtime base generations (its
//	                           process environment) and, for each base, this
//	                           node's cache marker and the shared
//	                           publication, judged as ensureBaseGeneration
//	                           will. Exit 0: every base is cached here,
//	                           published at its configured generation, or
//	                           never published (no deployment needs it yet);
//	                           3: a base's shared copy is another generation
//	                           and the cache does not hold the configured one,
//	                           so every boot of it here would fail 503
//	                           scan_critical (keep the node out of service);
//	                           1: the check itself failed.
//
// It prints one JSON object on stdout. rc.293 on production-us
// (2026-10-24): fsn-2's pre-stage published nothing, the shared scan sidecar
// kept naming the previous base-minimal generation, and the readiness gate
// still returned fsn-2 to service; every wake there failed.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/imaged"
)

const (
	baseAdoptionExitAdoptable = 0
	baseAdoptionExitStale     = 3
)

type baseAdoptionReport struct {
	// Adoptable: no configured base is stale on this node.
	Adoptable bool                  `json:"adoptable"`
	Bases     []imaged.BaseAdoption `json:"bases"`
}

func (r baseAdoptionReport) exitCode() int {
	if r.Adoptable {
		return baseAdoptionExitAdoptable
	}
	return baseAdoptionExitStale
}

// judgeBaseAdoptions judges every configured base, in key order.
func judgeBaseAdoptions(configured map[string]string, judge func(key, generation string) (imaged.BaseAdoption, error)) (baseAdoptionReport, error) {
	keys := make([]string, 0, len(configured))
	for key := range configured {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	report := baseAdoptionReport{Adoptable: true, Bases: make([]imaged.BaseAdoption, 0, len(keys))}
	for _, key := range keys {
		adoption, err := judge(key, configured[key])
		if err != nil {
			return baseAdoptionReport{}, fmt.Errorf("judge %s: %w", key, err)
		}
		if !adoption.Adoptable() {
			report.Adoptable = false
		}
		report.Bases = append(report.Bases, adoption)
	}
	return report, nil
}

func baseAdoptionCheckCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("base-adoption-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	servingPID := fs.Int("serving-pid", 0, "the serving vmmd's process id; its environment is the configuration to check")
	servingEnviron := fs.String("serving-environ", "", "a NUL-separated environment file to read instead of /proc/<serving-pid>/environ")
	timeout := fs.Duration("timeout", time.Minute, "how long opening storage and reading the shared publications may take")
	if err := fs.Parse(args); err != nil {
		return adoptionExitError
	}
	report, err := runBaseAdoptionCheck(*servingPID, *servingEnviron, *timeout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vmmd base-adoption-check: %v\n", err)
		return adoptionExitError
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return adoptionExitError
	}
	return report.exitCode()
}

func runBaseAdoptionCheck(pid int, environPath string, timeout time.Duration) (baseAdoptionReport, error) {
	servingEnv, err := readServingEnviron(pid, environPath)
	if err != nil {
		return baseAdoptionReport{}, err
	}
	configured, err := configuredRuntimeBaseGenerations(mapLookup(servingEnv))
	if err != nil {
		return baseAdoptionReport{}, fmt.Errorf("serving generations: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	be, err := baseGenerationStorage(ctx, servingEnv)
	if err != nil {
		return baseAdoptionReport{}, fmt.Errorf("open the serving storage backend: %w", err)
	}
	return judgeBaseAdoptions(configured, func(key, generation string) (imaged.BaseAdoption, error) {
		return imaged.JudgeBaseAdoption(ctx, be, key, generation)
	})
}
