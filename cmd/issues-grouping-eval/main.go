package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/issues/evaluation"
)

func main() {
	file := flag.String("corpus", "pkg/issues/evaluation/testdata/corpus.json", "labeled grouping corpus")
	strategy := flag.String("strategy", "all", "legacy, failure-site-v1, cause-chain-v1 or all")
	flag.Parse()
	input, err := os.Open(*file)
	if err != nil {
		fail(err)
	}
	defer input.Close()
	raw, err := io.ReadAll(io.LimitReader(input, evaluation.MaxCorpusBytes+1))
	if err != nil {
		fail(err)
	}
	if len(raw) > evaluation.MaxCorpusBytes {
		fail(fmt.Errorf("corpus exceeds byte bound"))
	}
	var cases []evaluation.Case
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cases); err != nil {
		fail(err)
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		fail(fmt.Errorf("expected one corpus array"))
	}
	strategies := []string{*strategy}
	if *strategy == "all" {
		strategies = []string{"legacy", "failure-site-v1", "cause-chain-v1"}
	}
	reports := []evaluation.Report{}
	now := time.Now().UTC()
	for _, name := range strategies {
		report, err := evaluation.Evaluate(cases, name, now)
		if err != nil {
			fail(err)
		}
		reports = append(reports, report)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(reports); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
