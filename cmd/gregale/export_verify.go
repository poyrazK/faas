package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Only integrity claims are read from the receipt. Its optional path and any
// future extensions are ignored; they never choose a file or enter output.
type exportIntegrityReceipt struct {
	Format         string
	Published      bool
	PagesCompleted int64
	ItemsEmitted   int64
	BytesWritten   int64
	SHA256         string
}

type exportIntegrityChecks struct {
	SHA256       bool `json:"sha256"`
	BytesWritten bool `json:"bytes_written"`
	ItemsEmitted bool `json:"items_emitted"`
}

type exportIntegrityObserved struct {
	SHA256       string `json:"sha256"`
	BytesWritten int64  `json:"bytes_written"`
	ItemsEmitted int64  `json:"items_emitted"`
}

type exportVerification struct {
	SchemaVersion int                     `json:"schema_version"`
	Verified      bool                    `json:"verified"`
	Format        string                  `json:"format"`
	Checks        exportIntegrityChecks   `json:"checks"`
	Observed      exportIntegrityObserved `json:"observed"`
}

func readExportIntegrityReceipt(r io.Reader) (exportIntegrityReceipt, error) {
	var receipt exportIntegrityReceipt
	decoder := json.NewDecoder(r)
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return receipt, errors.New("receipt must be one JSON object")
	}
	fields := make(map[string]json.RawMessage)
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return receipt, errors.New("receipt contains invalid or duplicate fields")
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return receipt, errors.New("receipt contains invalid JSON")
		}
		switch name {
		case "format", "published", "pages_completed", "items_emitted", "bytes_written", "sha256":
			fields[name] = value
		}
	}
	if _, err := decoder.Token(); err != nil {
		return receipt, errors.New("receipt contains incomplete JSON")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return receipt, errors.New("receipt must contain exactly one JSON object")
	}
	for _, field := range []struct {
		name string
		dst  any
	}{
		{"format", &receipt.Format}, {"published", &receipt.Published},
		{"pages_completed", &receipt.PagesCompleted}, {"items_emitted", &receipt.ItemsEmitted},
		{"bytes_written", &receipt.BytesWritten}, {"sha256", &receipt.SHA256},
	} {
		value, ok := fields[field.name]
		if !ok || strings.TrimSpace(string(value)) == "null" || json.Unmarshal(value, field.dst) != nil {
			return receipt, fmt.Errorf("receipt requires a valid %s field", field.name)
		}
	}
	if receipt.Format != "jsonl" || !receipt.Published {
		return receipt, errors.New("receipt must describe a published jsonl export")
	}
	if receipt.PagesCompleted < 0 || receipt.ItemsEmitted < 0 || receipt.BytesWritten < 0 {
		return receipt, errors.New("receipt counts must be nonnegative integers")
	}
	digest, err := hex.DecodeString(receipt.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return receipt, errors.New("receipt sha256 must contain 64 hexadecimal characters")
	}
	receipt.SHA256 = strings.ToLower(receipt.SHA256)
	return receipt, nil
}

// At most one line is retained. Decode no API row schema or numeric values:
// integrity covers the exact input bytes, including line endings and spaces.
func observeExportIntegrity(r io.Reader) (exportIntegrityObserved, error) {
	var observed exportIntegrityObserved
	hash := sha256.New()
	reader := bufio.NewReader(r)
	for lineNumber := int64(1); ; lineNumber++ {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if !utf8.Valid(line) || !json.Valid(line) {
				return observed, fmt.Errorf("invalid JSON Lines record at line %d", lineNumber)
			}
			_, _ = hash.Write(line)
			observed.BytesWritten += int64(len(line))
			observed.ItemsEmitted++
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return observed, fmt.Errorf("read export: %w", err)
			}
			observed.SHA256 = hex.EncodeToString(hash.Sum(nil))
			return observed, nil
		}
	}
}

func verifyExportIntegrity(r io.Reader, receipt exportIntegrityReceipt) (exportVerification, error) {
	observed, err := observeExportIntegrity(r)
	if err != nil {
		return exportVerification{}, err
	}
	checks := exportIntegrityChecks{SHA256: observed.SHA256 == receipt.SHA256,
		BytesWritten: observed.BytesWritten == receipt.BytesWritten, ItemsEmitted: observed.ItemsEmitted == receipt.ItemsEmitted}
	return exportVerification{SchemaVersion: 1, Verified: checks.SHA256 && checks.BytesWritten && checks.ItemsEmitted,
		Format: "jsonl", Checks: checks, Observed: observed}, nil
}

func openExportVerificationInput(path, label string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", label)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("%s changed while opening", label)
	}
	return file, nil
}

func verifyExportFiles(path, receiptPath string) (exportVerification, error) {
	receiptFile, err := openExportVerificationInput(receiptPath, "receipt")
	if err != nil {
		return exportVerification{}, err
	}
	defer receiptFile.Close()
	receipt, err := readExportIntegrityReceipt(receiptFile)
	if err != nil {
		return exportVerification{}, err
	}
	file, err := openExportVerificationInput(path, "export")
	if err != nil {
		return exportVerification{}, err
	}
	defer file.Close()
	return verifyExportIntegrity(file, receipt)
}

func writeExportVerification(w io.Writer, result exportVerification, jsonMode bool) error {
	var data []byte
	if jsonMode {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		data = append(encoded, '\n')
	} else if result.Verified {
		data = fmt.Appendf(nil, "Export verified: %d records, %d bytes, SHA-256 %s\n", result.Observed.ItemsEmitted, result.Observed.BytesWritten, result.Observed.SHA256)
	} else {
		data = fmt.Appendf(nil, "Export verification failed: sha256=%t bytes_written=%t items_emitted=%t\n", result.Checks.SHA256, result.Checks.BytesWritten, result.Checks.ItemsEmitted)
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
