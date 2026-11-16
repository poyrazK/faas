package main_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func integrityReceiptJSON(data string, count int64) string {
	digest := sha256.Sum256([]byte(data))
	b, err := json.Marshal(map[string]any{"format": "jsonl", "published": true, "pages_completed": 1,
		"items_emitted": count, "bytes_written": len(data), "sha256": hex.EncodeToString(digest[:]), "path": "ignored-credential-path", "future": map[string]any{"version": 2}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestExportIntegrityExactBytesAndRecords(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		count      int64
	}{
		{"empty", "", 0}, {"rows", "{\"id\":9007199254740993}\n{\"id\":2}\n", 2},
		{"crlf", " {\"id\":1} \r\n", 1}, {"last-without-newline", "{\"id\":1}", 1},
		{"values", "null\ntrue\n123456789012345678901234567890\n\"text\"\n[]\n", 5},
		{"large-record", "{\"value\":\"" + strings.Repeat("x", 256*1024) + "\"}\n", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			receipt, err := main.ReadExportIntegrityReceipt(strings.NewReader(integrityReceiptJSON(tt.data, tt.count)))
			if err != nil {
				t.Fatal(err)
			}
			result, err := main.VerifyExportIntegrity(strings.NewReader(tt.data), receipt)
			if err != nil || !result.Verified || result.SchemaVersion != 1 || result.Format != "jsonl" || result.Observed.ItemsEmitted != tt.count || result.Observed.BytesWritten != int64(len(tt.data)) {
				t.Fatalf("%+v %v", result, err)
			}
			var output bytes.Buffer
			if err := main.WriteExportVerification(&output, result, true); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "credential") || strings.Contains(output.String(), "9007199254740993") {
				t.Fatal(output.String())
			}
			var object map[string]json.RawMessage
			if json.Unmarshal(output.Bytes(), &object) != nil || len(object) != 5 {
				t.Fatal(output.String())
			}
			var observed map[string]json.RawMessage
			if json.Unmarshal(object["observed"], &observed) != nil || len(observed) != 3 || string(observed["items_emitted"]) != jsonNumber(tt.count) {
				t.Fatal(output.String())
			}
		})
	}
}

func jsonNumber(value int64) string { b, _ := json.Marshal(value); return string(b) }

func TestExportIntegrityMismatchChecksAreIndependent(t *testing.T) {
	data := "{\"id\":1}\n"
	for _, field := range []string{"sha256", "bytes_written", "items_emitted"} {
		t.Run(field, func(t *testing.T) {
			r, err := main.ReadExportIntegrityReceipt(strings.NewReader(integrityReceiptJSON(data, 1)))
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "sha256":
				r.SHA256 = strings.Repeat("0", 64)
			case "bytes_written":
				r.BytesWritten++
			case "items_emitted":
				r.ItemsEmitted++
			}
			result, err := main.VerifyExportIntegrity(strings.NewReader(data), r)
			if err != nil || result.Verified || result.Checks.SHA256 != (field != "sha256") || result.Checks.BytesWritten != (field != "bytes_written") || result.Checks.ItemsEmitted != (field != "items_emitted") {
				t.Fatalf("%+v %v", result, err)
			}
			var output bytes.Buffer
			if err := main.WriteExportVerification(&output, result, false); err != nil || !strings.HasPrefix(output.String(), "Export verification failed:") {
				t.Fatalf("%s %v", output.String(), err)
			}
		})
	}
}

func TestExportIntegrityRejectsInvalidReceiptsWithoutValues(t *testing.T) {
	valid := integrityReceiptJSON("", 0)
	var original map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &original); err != nil {
		t.Fatal(err)
	}
	cases := []string{"", "[]", "null", valid + valid, valid + "credential-secret", "{\"secret\":\"credential-secret\"", strings.Replace(valid, "{", "{\"format\":\"jsonl\",", 1)}
	for _, field := range []string{"format", "published", "pages_completed", "items_emitted", "bytes_written", "sha256"} {
		for _, value := range []string{"missing", "null", " \n null ", "\"credential-secret\"", "1.5", "-1", "9223372036854775808"} {
			copy := make(map[string]json.RawMessage)
			for k, v := range original {
				copy[k] = v
			}
			if value == "missing" {
				delete(copy, field)
			} else {
				copy[field] = json.RawMessage(value)
			}
			b, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, string(b))
		}
	}
	cases = append(cases, strings.Replace(valid, "true", "false", 1))
	for i, input := range cases {
		if _, err := main.ReadExportIntegrityReceipt(strings.NewReader(input)); err == nil || strings.Contains(err.Error(), "credential-secret") {
			t.Fatalf("case %d err=%v", i, err)
		}
	}
	// Uppercase digests and future fields are accepted, without trusting path.
	var uppercase map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &uppercase); err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := json.Unmarshal(uppercase["sha256"], &digest); err != nil {
		t.Fatal(err)
	}
	uppercase["sha256"], _ = json.Marshal(strings.ToUpper(digest))
	encoded, err := json.Marshal(uppercase)
	if err != nil {
		t.Fatal(err)
	}
	upperReceipt, err := main.ReadExportIntegrityReceipt(bytes.NewReader(encoded))
	if err != nil || upperReceipt.SHA256 != digest {
		t.Fatalf("%+v %v", upperReceipt, err)
	}
	large := strings.Replace(valid, "\"items_emitted\":0", "\"items_emitted\":9007199254740993", 1)
	r, err := main.ReadExportIntegrityReceipt(strings.NewReader(large))
	if err != nil || r.ItemsEmitted != 9007199254740993 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestExportIntegrityBadJSONLinesAndReadFailures(t *testing.T) {
	for _, data := range []string{"\n", "{}\n\n", "{} {}\n", "{\n\"secret\":\"credential-secret\"}\n", "{}\n{\"secret\":credential-secret}\n", "{}\n\"\xff\"\n"} {
		_, err := main.ObserveExportIntegrity(strings.NewReader(data))
		if err == nil || !strings.Contains(err.Error(), "line ") || strings.Contains(err.Error(), "credential-secret") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("err=%v", err)
		}
	}
	failure := errors.New("read failed")
	_, err := main.ObserveExportIntegrity(io.MultiReader(strings.NewReader("{}\n"), integrityFailReader{failure}))
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

type integrityFailReader struct{ err error }

func (r integrityFailReader) Read([]byte) (int, error) { return 0, r.err }

type integrityShortWriter struct{}

func (integrityShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type integrityFailWriter struct{}

func (integrityFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportIntegrityFilesAndOutputFailures(t *testing.T) {
	dir := t.TempDir()
	data := "{\"credential-secret\":1}\n"
	path, receiptPath := filepath.Join(dir, "rows.jsonl"), filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, []byte(integrityReceiptJSON(data, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := main.VerifyExportFiles(path, receiptPath)
	if err != nil || !result.Verified {
		t.Fatalf("%+v %v", result, err)
	}
	for _, jsonMode := range []bool{false, true} {
		if err := main.WriteExportVerification(integrityShortWriter{}, result, jsonMode); !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
		if err := main.WriteExportVerification(integrityFailWriter{}, result, jsonMode); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
	if _, err := main.VerifyExportFiles(dir, receiptPath); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := main.VerifyExportFiles(path, dir); err == nil {
		t.Fatal("receipt directory accepted")
	}
	if _, err := main.VerifyExportFiles(path, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing receipt accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != data {
		t.Fatal("input changed")
	}
}

func TestExportVerificationRejectsInputSymlinks(t *testing.T) {
	directory := t.TempDir()
	path, receipt := filepath.Join(directory, "rows.jsonl"), filepath.Join(directory, "receipt.json")
	data := "{}\n"
	for name, contents := range map[string]string{path: data, receipt: integrityReceiptJSON(data, 1)} {
		if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{path, receipt, filepath.Join(directory, "missing")} {
		link := filepath.Join(t.TempDir(), "linked-input")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		left, right := path, receipt
		if target == receipt {
			right = link
		} else {
			left = link
		}
		if _, err := main.VerifyExportFiles(left, right); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	// Existing platform policy allows symlinked parent directories, such as /tmp
	// on macOS; only the customer-named final component must be a regular file.
	parentLink := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(directory, parentLink); err != nil {
		t.Fatal(err)
	}
	result, err := main.VerifyExportFiles(filepath.Join(parentLink, "rows.jsonl"), filepath.Join(parentLink, "receipt.json"))
	if err != nil || !result.Verified {
		t.Fatalf("parent symlink refused: %+v %v", result, err)
	}
}
