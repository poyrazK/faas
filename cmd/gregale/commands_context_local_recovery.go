package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type localRecoveryIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type localRecoveryConfig struct {
	Path                       string               `json:"path"`
	Status                     string               `json:"status"`
	Jobs                       int                  `json:"jobs"`
	Workflows                  int                  `json:"workflows"`
	CurrentConnectionJobs      int                  `json:"current_connection_jobs"`
	CurrentConnectionWorkflows int                  `json:"current_connection_workflows"`
	ScannedRecords             int                  `json:"scanned_records"`
	ScanComplete               bool                 `json:"scan_complete"`
	Issues                     []localRecoveryIssue `json:"issues"`
	InspectCommands            []string             `json:"inspect_commands"`
}

// Read the same bounded record format as recovery, continuing after bad files
// so the user can review every identified path without exposing file contents.
func inspectLocalRecoveryConfig(path, command string) localRecoveryConfig {
	report := localRecoveryConfig{Path: path, Status: "missing", ScanComplete: true, Issues: []localRecoveryIssue{}, InspectCommands: []string{}}
	profile, endpoint := currentProfile(), apiBase()
	issue := func(path, reason string) {
		report.Status = "invalid"
		report.Issues = append(report.Issues, localRecoveryIssue{path, reason})
	}
	directory, err := inspectArtifactOutputDirectory(path)
	if err != nil {
		issue(path, "Directory could not be safely inspected; use real directories, not symlinks.")
		report.ScanComplete = false
		return report
	}
	defer directory.Close()
	if directory.root == nil {
		return report
	}
	report.Status = "configured"
	folder, err := directory.root.OpenFile(".", os.O_RDONLY, 0)
	if err != nil {
		issue(path, "Directory could not be read.")
		report.ScanComplete = false
		return report
	}
	defer func() { _ = folder.Close() }()
	entriesSeen := 0
	for {
		entries, readErr := folder.ReadDir(128)
		for _, entry := range entries {
			entriesSeen++
			if entriesSeen > 10000 {
				issue(path, "Directory scan stopped at 10000 entries; counts are partial.")
				report.ScanComplete = false
				break
			}
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			if report.ScannedRecords == 1000 {
				issue(path, "More than 1000 JSON records; recovery requires reviewing this limit. Counts are partial.")
				report.ScanComplete = false
				break
			}
			report.ScannedRecords++
			filePath := filepath.Join(path, entry.Name())
			record, reason := inspectLocalRecoveryRecord(directory.root, entry.Name())
			if reason != "" {
				issue(filePath, reason)
				continue
			}
			if record.Kind == "job" {
				report.Jobs++
				if record.Profile == profile && record.API == endpoint {
					report.CurrentConnectionJobs++
				}
			} else {
				report.Workflows++
				if record.Profile == profile && record.API == endpoint {
					report.CurrentConnectionWorkflows++
				}
			}
		}
		if !report.ScanComplete || errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			issue(path, "Directory scan failed; counts are partial.")
			report.ScanComplete = false
			break
		}
	}
	sort.Slice(report.Issues, func(i, j int) bool { return report.Issues[i].Path < report.Issues[j].Path })
	if report.Status == "configured" {
		if report.CurrentConnectionJobs > 0 {
			report.InspectCommands = append(report.InspectCommands, command+"jobs resume --list --current-connection")
		}
		if report.CurrentConnectionWorkflows > 0 {
			report.InspectCommands = append(report.InspectCommands, command+"workflows resume-wait --list --current-connection")
		}
	}
	return report
}

func inspectLocalRecoveryRecord(root *os.Root, name string) (acceptedRunRecord, string) {
	var record acceptedRunRecord
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return record, "Record must be a readable regular file, not a symlink or directory."
	}
	if info.Size() > 8192 {
		return record, "Record exceeds the 8 KiB limit."
	}
	data, err := readCustomerOperationSourceFile(root, name, 8192)
	if err != nil {
		return record, "Record could not be safely read within the 8 KiB limit."
	}
	if json.Unmarshal(data, &record) != nil {
		return record, "Record contains malformed JSON."
	}
	if validateAcceptedRunRecord(record) != nil {
		return record, "Record identity, version, workload, or connection is invalid."
	}
	return record, ""
}

func renderLocalRecoveryConfig(report localRecoveryConfig) {
	_, _ = fmt.Fprintf(osStdout, "Saved recovery records: %s\n  Path: %s\n  Valid Job records: %d; valid Workflow records: %d (all recorded connections).\n  JSON records checked: %d; scan complete: %t\n", report.Status, oneLine(report.Path), report.Jobs, report.Workflows, report.ScannedRecords, report.ScanComplete)
	_, _ = fmt.Fprintf(osStdout, "  Current profile/API: %d Job records; %d Workflow records (local comparison only).\n", report.CurrentConnectionJobs, report.CurrentConnectionWorkflows)
	for _, issue := range report.Issues {
		_, _ = fmt.Fprintf(osStdout, "  Review: %s\n    %s\n", oneLine(issue.Path), issue.Reason)
	}
	for _, command := range report.InspectCommands {
		_, _ = fmt.Fprintf(osStdout, "  Inspect (POSIX shells): %s\n", command)
	}
	_, _ = fmt.Fprintln(osStdout, "  Every record was retained; no remote status was read. Counts include only validated records.")
}
