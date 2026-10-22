package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues/evaluation"
)

var errRegression = errors.New("grouping regression: new false-merge or false-split pairs; review the comparison")

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	err := execute(args, stdout, stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, err)
	if errors.Is(err, errRegression) {
		return 2
	}
	return 1
}

func execute(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("issues-grouping-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("corpus", "pkg/issues/evaluation/testdata/corpus.json", "reviewed labeled grouping corpus")
	strategy := flags.String("strategy", "all", "legacy, failure-site-v1, cause-chain-v1 or all (aggregate mode only)")
	baselineFile := flags.String("baseline", "", "reviewed baseline to compare with the candidate strategy")
	compareWith := flags.String("compare-with", "", "baseline strategy for a preview without a saved snapshot")
	writeFile := flags.String("write-baseline", "", "create a private baseline file for review (refuses overwrite)")
	check := flags.Bool("check", false, "exit 2 for newly incorrect pairs compared with -baseline")
	if err := flags.Parse(args); err != nil {
		return err
	}
	modes := 0
	for _, value := range []string{*baselineFile, *compareWith, *writeFile} {
		if value != "" {
			modes++
		}
	}
	if flags.NArg() != 0 || modes > 1 || (modes > 0 && *strategy == "all") || (*check && *baselineFile == "") {
		return fmt.Errorf("choose one of -baseline, -compare-with or -write-baseline with one -strategy; -check requires -baseline; no positional arguments")
	}
	raw, err := readDocument(*file, evaluation.MaxCorpusBytes)
	if err != nil {
		return fmt.Errorf("read corpus: %w", err)
	}
	cases, err := evaluation.DecodeCorpus(raw)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if *writeFile != "" {
		baseline, err := evaluation.Snapshot(cases, *strategy, now)
		if err != nil {
			return err
		}
		if err := writeBaseline(*writeFile, baseline); err != nil {
			return err
		}
		// Report aggregate metrics; the saved file is reviewed separately.
	} else if *baselineFile != "" || *compareWith != "" {
		baseline, err := loadBaseline(cases, *baselineFile, *compareWith, now)
		if err != nil {
			return err
		}
		comparison, err := evaluation.Compare(cases, baseline, *strategy, now)
		if err != nil {
			return err
		}
		if err := encode(stdout, comparison); err != nil {
			return err
		}
		if *check && comparison.Regressed {
			return errRegression
		}
		return nil
	}
	strategies := []string{*strategy}
	if *strategy == "all" {
		strategies = []string{"legacy", "failure-site-v1", "cause-chain-v1"}
	}
	reports := []evaluation.Report{}
	for _, name := range strategies {
		report, err := evaluation.Evaluate(cases, name, now)
		if err != nil {
			return err
		}
		reports = append(reports, report)
	}
	return encode(stdout, reports)
}

func loadBaseline(cases []evaluation.Case, path, strategy string, now time.Time) (evaluation.Baseline, error) {
	if path == "" {
		return evaluation.Snapshot(cases, strategy, now)
	}
	raw, err := readDocument(path, api.IssueGroupingEvaluationBaselineMaxBytes)
	if err != nil {
		return evaluation.Baseline{}, fmt.Errorf("read baseline: %w", err)
	}
	return evaluation.DecodeBaseline(raw)
}

func encode(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write grouping report: %w", err)
	}
	return nil
}

func readDocument(path string, bound int) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(bound) {
		return nil, fmt.Errorf("input must be a bounded regular file, not a symlink")
	}
	input, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close() }()
	opened, err := input.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("input changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(input, int64(bound)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > bound {
		return nil, fmt.Errorf("input exceeds byte bound")
	}
	return raw, nil
}

func writeBaseline(path string, baseline evaluation.Baseline) error {
	raw, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	raw = append(raw, '\n')
	if len(raw) > api.IssueGroupingEvaluationBaselineMaxBytes {
		return fmt.Errorf("baseline exceeds byte bound")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(path)
	output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create baseline (choose a new path; existing files are never replaced): %w", err)
	}
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = root.Remove(name)
		}
	}()
	if _, err := output.Write(raw); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync baseline: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close baseline: %w", err)
	}
	complete = true
	return nil
}
