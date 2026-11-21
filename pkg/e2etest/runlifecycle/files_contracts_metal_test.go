//go:build metal

package runlifecycle_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSourceFilesContractsMetal uses the real public transfer/admission API,
// database, scheduler, vmmd, guest interpreter, CLI, and physical leak checks.
func TestSourceFilesContractsMetal(t *testing.T) {
	t.Setenv("FAAS_RUN_FILE_QUALIFICATION", "1")
	f := newRunFixture(t)
	data := bytes.Repeat([]byte("gregale-data-42\n"), 786432) // 12 MiB; exceeds the 1 MiB JSON/source limit.
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	var input api.ExecutionFileResponse
	request := runRequest(`import fs from 'node:fs'; import {pipeline} from 'node:stream/promises'; export default async (_,c) => { const input=c.input_dir+'/data.bin'; await pipeline(fs.createReadStream(input),fs.createWriteStream(c.output_dir+'/copy.bin')); return {bytes:fs.statSync(input).size,uid:process.getuid()}; }`)
	request.InputSchema = json.RawMessage(`{"type":"object","required":["value"]}`)
	request.ResultSchema = json.RawMessage(`{"type":"object","required":["bytes","uid"],"properties":{"bytes":{"type":"integer"},"uid":{"const":1000}}}`)
	request.ArtifactStorage = api.ExecutionArtifactStored
	request.OutputFiles = []string{"copy.bin"}
	request.Limits.MaxOutputBytes = 4096
	request.Limits.MaxArtifactBytes = int64(len(data))
	if !t.Run("prepare_and_missing_input_never_admits", func(t *testing.T) {
		var err error
		input, err = f.client.PrepareExecutionFile(t.Context(), api.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest})
		if err != nil || !input.UploadRequired {
			t.Fatal("prepare", err)
		}
		request.InputFiles = []api.ExecutionFileInput{{FileID: input.ID, Path: "data.bin"}}
		_, err = f.client.CreateExecution(api.ContextWithIdempotencyKey(t.Context(), "files-cold"), request)
		var p *api.APIError
		if !errors.As(err, &p) || p.Problem.Status != 409 || p.Problem.Code != "execution_files_not_ready" {
			t.Fatal("missing input did not reject admission", err)
		}
		var count int
		if err = f.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM executions WHERE account_id=$1", f.accountID).Scan(&count); err != nil || count != 0 {
			t.Fatal("missing input created a Run", count, err)
		}
	}) {
		return
	}
	if !t.Run("verified_upload_is_immutable_and_family_scoped", func(t *testing.T) {
		ready, err := f.client.UploadExecutionFile(t.Context(), input.ID, bytes.NewReader(data), int64(len(data)), digest)
		if err != nil || ready.Status != "ready" {
			t.Fatal("upload", err)
		}
		again, err := f.client.PrepareExecutionFile(t.Context(), api.CreateExecutionFileRequest{SizeBytes: int64(len(data)), SHA256: digest})
		if err != nil || again.ID != input.ID || again.UploadRequired {
			t.Fatal("stable identity", err)
		}
		token, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = state.NewPgStore(f.h.Pool).CreateAPIKey(t.Context(), f.accountID, hash, "other-file-family", []string{api.ScopeRunsRead, api.ScopeRunsWrite}); err != nil {
			t.Fatal(err)
		}
		_, err = api.NewClient(f.h.APIDURL, token).GetExecutionFile(t.Context(), input.ID)
		var p *api.APIError
		if !errors.As(err, &p) || p.Problem.Status != 404 {
			t.Fatal("another key family read the file", err)
		}
	}) {
		return
	}
	var original api.ExecutionResponse
	if !t.Run("cold_large_file_roundtrip_and_valid_contract", func(t *testing.T) {
		original = f.wait(t, f.submit(t, "files-cold", request).ID, api.ExecutionStatusSucceeded)
		f.requireWakeMethod(t, original.ID, "cold_boot")
		if original.ResultValidation == nil || original.ResultValidation.Status != "valid" || len(original.Artifacts) != 1 || original.Artifacts[0].FileID == "" || len(original.Artifacts[0].Content) != 0 || original.Artifacts[0].SHA256 != digest {
			t.Fatal("stored receipt or validation is invalid")
		}
		var result struct {
			Bytes int64
			UID   int
		}
		if json.Unmarshal(original.Result, &result) != nil || result.Bytes != int64(len(data)) || result.UID != 1000 {
			t.Fatal("guest input was not staged correctly")
		}
		hash := sha256.New()
		meta, err := f.client.DownloadExecutionFile(t.Context(), original.Artifacts[0].FileID, hash)
		if err != nil || meta.SizeBytes != int64(len(data)) || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
			t.Fatal("download", err)
		}
	}) {
		return
	}
	if !t.Run("warm_clones_get_independent_input_files", func(t *testing.T) {
		f.capturePristine(t)
		req := runRequest(`import fs from 'node:fs'; export default async (_,c)=>{const p=c.input_dir+'/data.bin';const b=fs.readFileSync(p);fs.writeFileSync(p,'changed');console.log('running-marker');await new Promise(resolve=>setTimeout(resolve,5000));return {original:b.length,prefix:b.subarray(0,15).toString()};}`)
		req.InputFiles = request.InputFiles
		first := f.submit(t, "files-clone-a", req)
		second := f.submit(t, "files-clone-b", req)
		f.waitGuestStarted(t, first.ID)
		f.waitGuestStarted(t, second.ID)
		f.assertSimultaneouslyIsolated(t, []string{first.ID, second.ID})
		for _, run := range []api.ExecutionResponse{first, second} {
			row := f.wait(t, run.ID, api.ExecutionStatusSucceeded)
			f.requireWakeMethod(t, row.ID, "restore")
			var value struct {
				Original int
				Prefix   string
			}
			if json.Unmarshal(row.Result, &value) != nil || value.Original != len(data) || value.Prefix != string(data[:15]) {
				t.Fatal("clones shared or changed immutable input data")
			}
		}
	}) {
		return
	}
	if !t.Run("contract_failure_preserves_result_without_reexecution", func(t *testing.T) {
		req := runRequest(`export default ()=>({private_value:'contract-debug-value'})`)
		req.ResultSchema = json.RawMessage(`{"type":"integer"}`)
		row := f.wait(t, f.submit(t, "files-invalid-result", req).ID, api.ExecutionStatusSucceeded)
		if !row.ContractFailed() || !bytes.Contains(row.Result, []byte("contract-debug-value")) || row.ResultValidation.Message != "Run result does not match result_schema" {
			t.Fatal("contract failure changed execution status or lost the result")
		}
		replay := f.submit(t, "files-invalid-result", req)
		if replay.ID != row.ID || !replay.ContractFailed() {
			t.Fatal("contract failure was reexecuted")
		}
	}) {
		return
	}
	if !t.Run("cli_streams_local_files_and_downloads_stored_outputs", func(t *testing.T) {
		// Distinct bytes force a real CLI upload rather than reuse of the
		// immutable object published by the earlier API transfer.
		cliData := bytes.Clone(data)
		cliData[0] ^= 1
		cliSum := sha256.Sum256(cliData)
		cliDigest := "sha256:" + hex.EncodeToString(cliSum[:])
		path := filepath.Join(f.root, "cli-input.bin")
		if err := os.WriteFile(path, cliData, 0o600); err != nil {
			t.Fatal(err)
		}
		schema := filepath.Join(f.root, "result-schema.json")
		if err := os.WriteFile(schema, request.ResultSchema, 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(f.root, "cli-output")
		cmd := exec.CommandContext(t.Context(), filepath.Join(os.Getenv("FAAS_E2E_BIN_DIR"), "gregale"), "--json", "run", "--runtime", "node24", "--source", request.Source, "--input-file", "data.bin="+path, "--artifact-storage", "stored", "--output-file", "copy.bin", "--result-schema", schema, "--idempotency-key", "files-cli", "--memory-mb", "256", "--ephemeral-disk-mb", "64", "--timeout-ms", "30000", "--output-dir", out)
		cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "FAAS_CAPABILITY_FLAGS_RELEASE_ALL=1", "FAAS_API=" + f.h.APIDURL, "FAAS_TOKEN=" + f.client.Token(), "XDG_CONFIG_HOME=" + filepath.Join(f.root, "cli-config"), "XDG_CACHE_HOME=" + filepath.Join(f.root, "cli-cache")}
		stdout, err := cmd.Output()
		if err != nil {
			t.Fatal("CLI file Run failed", err)
		}
		var receipt api.ExecutionResponse
		if json.Unmarshal(stdout, &receipt) != nil || receipt.ID == "" || receipt.ContractFailed() {
			t.Fatal("CLI did not return a validated receipt")
		}
		f.runs[receipt.ID] = true
		root, err := os.OpenRoot(out)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		file, err := root.Open("copy.bin")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != cliDigest {
			t.Fatal("CLI output checksum mismatch", err)
		}
	}) {
		return
	}
	if !t.Run("revocation_and_apid_restart_keep_admission_replay", func(t *testing.T) {
		if err := f.client.DeleteExecutionFile(t.Context(), input.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.h.RestartAPID(); err != nil {
			t.Fatal(err)
		}
		replay := f.submit(t, "files-cold", request)
		if replay.ID != original.ID || replay.Status != api.ExecutionStatusSucceeded {
			t.Fatal("replay depended on the revoked input")
		}
	}) {
		return
	}
	t.Run("payload_erasure_single_usage_and_no_live_resources", func(t *testing.T) { f.assertSettled(t) })
}
