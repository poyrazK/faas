package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func forgetRecordedRun(kind, runID string, records []acceptedRunRecord) int {
	var selected []acceptedRunRecord
	for _, record := range records {
		if record.RunID == runID && record.Profile == currentProfile() && record.API == apiBase() {
			selected = append(selected, record)
		}
	}
	if len(selected) != 1 {
		return printErr("Cannot select local record", errors.New("select exactly one recorded run UUID with its recorded API and profile; use --list to inspect records"))
	}
	record := selected[0]
	if err := removeAcceptedRunRecord(record); err != nil {
		return printErr("Could not forget local run", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Forgotten     acceptedRunRecord `json:"forgotten"`
			RemoteChanged bool              `json:"remote_changed"`
		}{record, false}))
	}
	_, _ = fmt.Fprintf(osStdout, "Forgot local %s record for run %s. Remote execution and results were not changed.\n", kind, runID)
	return 0
}

func removeAcceptedRunRecord(record acceptedRunRecord) error {
	directory, err := acceptedRunDirectory()
	if err != nil {
		return err
	}
	// Pin real directory components before unlinking a local record.
	directoryHandle, err := inspectArtifactOutputDirectory(directory)
	if err != nil {
		return fmt.Errorf("inspect record directory: %w", err)
	}
	defer directoryHandle.Close()
	if directoryHandle.root == nil {
		return errors.New("record directory no longer exists")
	}
	name := acceptedRunRecordFilename(record)
	f, err := openCustomerFile(filepath.Join(directory, name))
	if err != nil {
		return fmt.Errorf("open selected record: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("inspect selected record: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return fmt.Errorf("read selected record: %w", err)
	}
	if len(data) > 8192 {
		return errors.New("selected record exceeds 8 KiB")
	}
	var current acceptedRunRecord
	if err := json.Unmarshal(data, &current); err != nil || current != record {
		return errors.New("selected record changed; list records again before forgetting")
	}
	latest, err := directoryHandle.root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect selected record before removal: %w", err)
	}
	if !latest.Mode().IsRegular() || !os.SameFile(info, latest) {
		return errors.New("selected record changed before removal")
	}
	if err := directoryHandle.root.Remove(name); err != nil {
		return fmt.Errorf("remove selected record: %w", err)
	}
	return nil
}
