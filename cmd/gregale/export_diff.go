package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"
)

type exportDiffEntry struct {
	digest [sha256.Size]byte
	key    json.RawMessage
	fields *exportDiffField
}

type exportDiffSummary struct {
	Before    int `json:"before"`
	After     int `json:"after"`
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

type exportDiffReport struct {
	SchemaVersion int                      `json:"schema_version"`
	Key           string                   `json:"key"`
	HasChanges    bool                     `json:"has_changes"`
	Summary       exportDiffSummary        `json:"summary"`
	Added         []json.RawMessage        `json:"added"`
	Removed       []json.RawMessage        `json:"removed"`
	Changed       []json.RawMessage        `json:"changed"`
	Explanations  *[]exportDiffExplanation `json:"explanations,omitempty"`
}

func readExportDiffIndex(reader io.Reader, key, label string) (map[string]exportDiffEntry, error) {
	return readExportDiffIndexMode(reader, key, label, false)
}

func readExportDiffIndexMode(reader io.Reader, key, label string, explain bool) (map[string]exportDiffEntry, error) {
	index := make(map[string]exportDiffEntry)
	lines := bufio.NewReader(reader)
	for lineNumber := 1; ; lineNumber++ {
		line, err := lines.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read %s export: %w", label, err)
		}
		if len(line) > 0 {
			entryID, entry, parseErr := parseExportDiffRecordMode(line, key, explain)
			if parseErr != nil {
				return nil, fmt.Errorf("%s export line %d: %w", label, lineNumber, parseErr)
			}
			if _, duplicate := index[entryID]; duplicate {
				return nil, fmt.Errorf("%s export line %d: duplicate record key", label, lineNumber)
			}
			index[entryID] = entry
		}
		if errors.Is(err, io.EOF) {
			return index, nil
		}
	}
}

func parseExportDiffRecordMode(line []byte, key string, explain bool) (string, exportDiffEntry, error) {
	if !utf8.Valid(line) || !json.Valid(line) || !exportDiffUnicodeValid(line) {
		return "", exportDiffEntry{}, errors.New("invalid UTF-8 JSON Lines record")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	value, err := readExportDiffValueMode(decoder, explain)
	if err != nil {
		return "", exportDiffEntry{}, err
	}
	if value.kind != 'o' {
		return "", exportDiffEntry{}, errors.New("record must be a JSON object")
	}
	selected, found := value.members[key]
	if !found || selected.kind != 'n' && selected.kind != 's' || selected.kind == 's' && selected.scalar == "" {
		return "", exportDiffEntry{}, errors.New("selected key must be a nonempty string or JSON number")
	}
	var encoded json.RawMessage
	if selected.kind == 's' {
		encoded, _ = json.Marshal(selected.scalar)
	} else {
		encoded = json.RawMessage(selected.scalar)
	}
	entry := exportDiffEntry{digest: value.digest, key: encoded}
	if explain {
		fields := exportDiffFields(value)
		entry.fields = &fields
	}
	return string(selected.kind) + selected.scalar, entry, nil
}

func compareExportDiffIndexes(before, after map[string]exportDiffEntry, key string) exportDiffReport {
	report := exportDiffReport{SchemaVersion: 1, Key: key, Summary: exportDiffSummary{Before: len(before), After: len(after)},
		Added: []json.RawMessage{}, Removed: []json.RawMessage{}, Changed: []json.RawMessage{}}
	keys := make([]string, 0, len(before)+len(after))
	for id := range before {
		keys = append(keys, id)
	}
	for id := range after {
		if _, found := before[id]; !found {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	for _, id := range keys {
		old, was := before[id]
		current, now := after[id]
		switch {
		case !was:
			report.Added = append(report.Added, current.key)
		case !now:
			report.Removed = append(report.Removed, old.key)
		case old.digest != current.digest:
			report.Changed = append(report.Changed, current.key)
		default:
			report.Summary.Unchanged++
		}
	}
	report.Summary.Added, report.Summary.Removed, report.Summary.Changed = len(report.Added), len(report.Removed), len(report.Changed)
	report.HasChanges = report.Summary.Added+report.Summary.Removed+report.Summary.Changed > 0
	return report
}

func diffExportFiles(beforePath, afterPath, key string) (exportDiffReport, error) {
	return diffExportFilesMode(beforePath, afterPath, key, false)
}

func diffExportFilesMode(beforePath, afterPath, key string, explain bool) (exportDiffReport, error) {
	beforeFile, err := openExportVerificationInput(beforePath, "before export")
	if err != nil {
		return exportDiffReport{}, err
	}
	defer beforeFile.Close()
	before, err := readExportDiffIndexMode(beforeFile, key, "before", explain)
	if err != nil {
		return exportDiffReport{}, err
	}
	afterFile, err := openExportVerificationInput(afterPath, "after export")
	if err != nil {
		return exportDiffReport{}, err
	}
	defer afterFile.Close()
	after, err := readExportDiffIndexMode(afterFile, key, "after", explain)
	if err != nil {
		return exportDiffReport{}, err
	}
	return compareExportDiffIndexesExplained(before, after, key, explain), nil
}

func writeExportDiff(w io.Writer, report exportDiffReport, jsonMode bool) error {
	var data []byte
	if jsonMode {
		encoded, err := json.Marshal(report)
		if err != nil {
			return err
		}
		data = append(encoded, '\n')
	} else {
		var output bytes.Buffer
		_, _ = fmt.Fprintf(&output, "Export diff: added=%d removed=%d changed=%d unchanged=%d\n", report.Summary.Added, report.Summary.Removed, report.Summary.Changed, report.Summary.Unchanged)
		for _, group := range []struct {
			name string
			keys []json.RawMessage
		}{{"Added", report.Added}, {"Removed", report.Removed}, {"Changed", report.Changed}} {
			for _, id := range group.keys {
				_, _ = fmt.Fprintf(&output, "%s: %s\n", group.name, id)
			}
		}
		appendExportDiffExplanations(&output, report.Explanations)
		data = output.Bytes()
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
