package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/api"
)

func lockAutomationLiveSuite(path string) (*flock.Flock, error) {
	if info, err := os.Lstat(path + ".lock"); err == nil && !info.Mode().IsRegular() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("invalid suite journal lock")
	}
	lock := flock.New(path+".lock", flock.SetPermissions(0600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		_ = lock.Close()
		return nil, errors.New("suite journal is busy or cannot be locked")
	}
	return lock, nil
}

func validateAutomationLiveSuite(j automationLiveSuiteJournal) error {
	if j.SchemaVersion != 1 || j.Revision == 0 || !validAutomationLiveSuiteSelection(j.Plan) || !automationLiveUUID(j.Plan.AppID) || j.Plan.CreatedAt.IsZero() || j.Plan.CreatedAt.After(time.Now().UTC()) || !validLiveSuiteHash(j.Plan.EndpointHash) || j.Fingerprint != liveSuiteHash(j.Plan) || len(j.Cases) != len(j.Plan.Cases) {
		return errors.New("invalid suite journal plan or fingerprint")
	}
	seenRuns := map[string]bool{}
	unstarted := false
	for i, c := range j.Cases {
		if c.Name != j.Plan.Cases[i] || c.Key != liveSuiteCaseKey(j.Plan, c.Name) || c.AttemptedAt != nil && (c.AttemptedAt.Before(j.Plan.CreatedAt) || c.AttemptedAt.After(time.Now().UTC())) {
			return errors.New("invalid suite case identity")
		}
		if c.RunID != "" && (!automationLiveUUID(c.RunID) || seenRuns[c.RunID]) || c.RunID != "" && c.AttemptedAt == nil || c.Verification != nil && (c.RunID == "" || !validAutomationLiveSuiteView(j.Plan, c, *c.Verification)) {
			return errors.New("invalid suite run identity")
		}
		if c.RunID != "" {
			seenRuns[c.RunID] = true
		}
		if unstarted && c.AttemptedAt != nil {
			return errors.New("suite contains admissions after an unstarted case")
		}
		if c.AttemptedAt == nil {
			if c.RunID != "" || c.Verification != nil || c.Outcome != "not_started" {
				return errors.New("invalid unstarted suite case")
			}
		} else {
			switch c.Outcome {
			case "starting", "pending", "passed", "failed", "outdated", "unavailable", "error", "timed_out", "interrupted":
			default:
				return errors.New("invalid suite case outcome")
			}
			if c.Outcome == "passed" && (c.Verification == nil || c.Verification.Outdated || !c.Verification.ReportCurrent || !c.Verification.Report.Passed) {
				return errors.New("invalid suite passing observation")
			}
		}
		unstarted = unstarted || c.AttemptedAt == nil
	}
	return nil
}

func readAutomationLiveSuite(path string) (automationLiveSuiteJournal, error) {
	var j automationLiveSuiteJournal
	f, err := openCustomerFile(path)
	if err != nil {
		return j, errors.New("could not open suite journal")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 || info.Size() > api.AutomationLiveSuiteJournalMaxBytes {
		return j, errors.New("suite journal must be a bounded private regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(f, api.AutomationLiveSuiteJournalMaxBytes+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&j) != nil {
		return j, errors.New("invalid suite journal JSON")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return j, errors.New("suite journal must contain exactly one object")
	}
	if err := validateAutomationLiveSuite(j); err != nil {
		return j, err
	}
	j.diskHash = liveSuiteHash(j)
	return j, nil
}

func saveAutomationLiveSuite(path string, j *automationLiveSuiteJournal, create bool) error {
	next := *j
	if next.Revision == ^uint64(0) {
		return errors.New("suite journal revision exhausted")
	}
	next.Revision++
	if err := validateAutomationLiveSuite(next); err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil || int64(len(data)+1) > api.AutomationLiveSuiteJournalMaxBytes {
		return errors.New("suite journal exceeds its size limit")
	}
	if create {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("could not create suite journal")
		}
		defer func() { _ = file.Close() }()
		if err := persistRecoveryRecord(file, next); err != nil {
			return errors.New("could not persist initial suite journal")
		}
		if err := file.Close(); err != nil {
			return err
		}
	} else {
		current, err := readAutomationLiveSuite(path)
		if err != nil || current.Revision != j.Revision || current.Fingerprint != j.Fingerprint || liveSuiteHash(current) != j.diskHash {
			return errors.New("suite journal changed; reload before continuing")
		}
		raw, _ := json.Marshal(current)
		if err := replaceAutomationLiveSuite(path, append(data, '\n'), raw); err != nil {
			return err
		}
	}
	next.diskHash = liveSuiteHash(next)
	*j = next
	return nil
}

func replaceAutomationLiveSuite(path string, data, previous []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".live-suite-*.tmp")
	if err != nil {
		return errors.New("could not stage suite journal")
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		return errors.New("could not sync suite journal")
	}
	current, err := readAutomationLiveSuite(path)
	raw, _ := json.Marshal(current)
	if err != nil || !bytes.Equal(raw, previous) {
		return errors.New("suite journal changed during persistence")
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return errors.New("could not replace suite journal")
	}
	if runtime.GOOS != "windows" {
		dir, err := openCustomerDirectoryForSync(filepath.Dir(path))
		if err != nil {
			return errors.New("could not open suite journal directory for sync")
		}
		defer func() { _ = dir.Close() }()
		if err := dir.Sync(); err != nil {
			return errors.New("could not sync suite journal directory")
		}
	}
	return nil
}
