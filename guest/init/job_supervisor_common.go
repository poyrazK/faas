// Package main — job-task supervisor wire types (issue #1184
// Workstream A / ADR-099).
//
// This file is build-tag-free: only the structs / constants / pure
// helpers that the linux file (job_supervisor_linux.go) and the
// darwin unit tests (job_supervisor_darwin_test.go, if any) need.
// The syscall.Exec / vsock / signal handling lives in the linux
// sibling; the type shapes mirror pkg/fcvm/job_vmm.go::JobManifest
// + JobExitPayload exactly so a wire-format drift would surface
// as a parse error on first cold boot.
//
// Mode priority at boot (decided in decideMode at
// main_linux.go:422): job > build > app. Job VMs carry
// /etc/faas/job.json AND /etc/faas/build.json is optional
// (never present on a job VM); build VMs never carry job.json;
// app VMs never carry job.json. So the precedence is "what file
// exists wins" — same pattern as the existing
// build.json-vs-app.json check.

package main

import (
	"os"
	"strings"
)

// VsockJobExitPort is the AF_VSOCK port the job supervisor dials
// on the host to ship the terminal exit envelope. Matches
// pkg/fcvm/job_vmm.go::VsockJobExitPort (1026). The port number
// is the same as VsockCharacterizationPort — the discriminator
// is the socket type (DGRAM for jobs, STREAM for characterize).
const VsockJobExitPort uint32 = 1026

// VsockJobExitMsgType is the wire-format discriminator for the
// job-exit envelope. Matches pkg/fcvm/job_vmm.go::VsockJobExitMsgType
// (=4). Distinct from VsockCharacterizationMsgType (=3) so a
// frame misrouted between the two channels is rejected at the
// host-side parse layer.
const VsockJobExitMsgType uint32 = 4

// VsockJobExitMaxBody caps the JSON body at 8 KiB. The exit
// envelope is tiny (exit_code + error_class + signal + lease_token
// + finished_at ≈ 200 bytes); 8 KiB is generous headroom. The
// supervisor hard-truncates BEFORE json.Marshal so the host never
// sees a malformed body. Matches pkg/fcvm/job_vmm.go::VsockJobExitMaxBody.
const VsockJobExitMaxBody = 8 * 1024

// JobManifest is the JSON shape vmmd writes to drive1 at
// /etc/faas/job.json. The supervisor reads it during boot phase
// 2 (after overlay + pivot, same as the app supervisor reads
// app.json). Field names match the host-side
// pkg/fcvm.JobManifest 1:1 so a wire-format drift surfaces as a
// decode error on the first cold boot of any job.
//
// `kind` is always "job" — the discriminator decideMode uses to
// route to runJob instead of runApp. Present even though the file
// is named job.json so a unit test driving decideMode via
// testing/fstest.MapFS can rely on the same field as the build
// path (which checks build.json's "kind":"build").
type JobManifest struct {
	Kind                string            `json:"kind"`
	AccountID           string            `json:"account_id,omitempty"`
	RunID               string            `json:"run_id,omitempty"`
	TaskIndex           int               `json:"task_index,omitempty"`
	LeaseToken          string            `json:"lease_token,omitempty"`
	ImageRef            string            `json:"image_ref,omitempty"`
	Command             []string          `json:"command,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	TaskTimeoutSec      int               `json:"task_timeout_s,omitempty"`
	VsockJobExitPort    int               `json:"vsock_job_exit_port,omitempty"`
	VsockJobExitMsgType int               `json:"vsock_job_exit_msg_type,omitempty"`
}

// JobExitPayload is the JSON the supervisor writes via vsock DGRAM
// at port 1026, msg_type 4 when the customer's command exits.
// Mirrors pkg/fcvm/job_vmm.go::JobExitPayload exactly — the wire
// format is the wire format (no separate DTO per transport).
//
// ErrorClass is the canonical mapped string the host's
// mapExitToTerminalStatus in pkg/sched/jobs.go reads; the
// canonical values are:
//
//	"succeeded"  // exit_code == 0
//	"failed"     // exit_code != 0 && < 128 → user_error in some flows
//	"timeout"    // exit_code == 124 (coreutils `timeout` sentinel)
//	"oom"        // exit_code == 137 (SIGKILL from kernel OOM-killer)
//	"cancelled"  // exit_code == 143 (SIGTERM, after 30s grace)
//	"infra"      // signal > 0 OR unknown error class
//
// Signal is the raw signal number from syscall.WaitStatus.Signal()
// if the process was killed by a signal, else 0.
// FinishedAtUnixNano is the wall-clock UnixNano at the moment
// the supervisor captured the exit (after the cmd.Wait() return).
type JobExitPayload struct {
	ExitCode           int32  `json:"exit_code"`
	ErrorClass         string `json:"error_class"`
	Signal             int32  `json:"signal"`
	FinishedAtUnixNano int64  `json:"finished_at_unix_nano"`
	LeaseToken         string `json:"lease_token,omitempty"`
}

// jobEnvBaseline are the env vars the supervisor ALWAYS sets
// (overriding any in m.Env) so the customer's command can identify
// itself for logging + observability. Matches the precedence
// rules in runAppWithEnv (systemEnv ⊕ job.Env ⊕ run.env_overrides,
// with systemEnv winning on conflict).
//
//nolint:unused // consumed by job_supervisor_linux.go on linux builds; macOS dev boxes don't see the consumer.
var jobEnvBaseline = map[string]string{
	"FAAS_JOB":          "1",
	"FAAS_RUNTIME_KIND": "job",
}

// buildEnvForJob merges os.Environ() with m.Env, then overlays
// the baseline (FAAS_JOB=1, FAAS_RUNTIME_KIND=job) so the customer's
// command can introspect "I'm running inside a job VM" without
// trusting the customer-supplied env. The customer's m.Env entries
// win on conflict with systemEnv (so FAAS_RUNTIME_KIND can be
// overridden if the customer really wants), but the baseline
// always wins on conflict with m.Env (so a malicious env can't
// unset FAAS_JOB).
//
// Returns the merged map converted to the []string form
// syscall.Exec wants (KEY=VAL pairs, no shell quoting needed
// because exec.Command takes the argv directly).
//
//nolint:unused // consumed by job_supervisor_linux.go on linux builds; macOS dev boxes don't see the consumer.
func buildEnvForJob(m JobManifest) []string {
	merged := make(map[string]string, len(os.Environ())+len(m.Env)+len(jobEnvBaseline))
	for _, kv := range os.Environ() {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			merged[kv[:eq]] = kv[eq+1:]
		}
	}
	for k, v := range m.Env {
		merged[k] = v
	}
	// Build a merged slice preserving the contract: FAAS_JOB ⊕
	// FAAS_RUNTIME_KIND win over both layers. The customer-supplied
	// m.Env is intentionally NOT allowed to unset these.
	for k, v := range jobEnvBaseline {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

// mapExitToErrorClass translates a (exit_code, signal) pair into
// the canonical wire-level error_class string. Mirrors
// pkg/sched/jobs.go::mapExitToTerminalStatus on the host side;
// drift between the two tables is a bug — a value the supervisor
// classifies as "timeout" but the host classifies as "failed" lands
// the task in the wrong terminal state and either spuriously
// retries or skips a legitimate retry.
//
// Mapping rules (in order):
//   - exit_code == 0 → "succeeded"
//   - signal > 0 → "infra" (process killed by signal; the signal
//     is in the payload's signal field for debugging)
//   - exit_code == 124 → "timeout" (coreutils `timeout` sentinel
//     — the supervisor fires SIGTERM at the task_timeout_s
//     deadline; if the process installs its own SIGTERM handler
//     and exits cleanly with that status, we still want the
//     scheduler to see "timeout" not "failed")
//   - exit_code == 137 → "oom" (kernel OOM-killer SIGKILL)
//   - exit_code == 143 → "cancelled" (SIGTERM, after 30s grace
//     from the supervisor's cancellation handler)
//   - exit_code in 129..159 → "infra" (signal-derived; the high
//     bit indicates signal death on POSIX)
//   - else → "failed" (any other non-zero exit)
//
// signal > 0 takes priority over the exit_code range because the
// POSIX exit code 128+N convention is what shells return for
// signal death; if the supervisor captured a signal, that's the
// authoritative story.
//
//nolint:unused // consumed by job_supervisor_linux.go on linux builds; macOS dev boxes don't see the consumer.
func mapExitToErrorClass(exitCode int32, signal int32) string {
	if exitCode == 0 {
		return "succeeded"
	}
	if signal > 0 {
		return "infra"
	}
	switch exitCode {
	case 124:
		return "timeout"
	case 137:
		return "oom"
	case 143:
		return "cancelled"
	}
	if exitCode >= 129 && exitCode <= 159 {
		return "infra"
	}
	return "failed"
}

// jobManifestPath is the canonical location vmmd writes the
// per-task manifest to on drive1. Mirror of the literal in
// pkg/fcvm/job_vmm.go::stageJobManifest. Drift here is a parse
// error on first cold boot.
//
//nolint:unused // consumed by job_supervisor_linux.go on linux builds; macOS dev boxes don't see the consumer.
const jobManifestPath = "etc/faas/job.json"
