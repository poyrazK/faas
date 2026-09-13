package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestExecutionPlanLimits(t *testing.T) {
	want := map[Plan]ExecutionPlanLimits{
		PlanFree: {},
		PlanHobby: {
			Allowed: true, MaxConcurrent: 1, MaxSourceBytes: 64 << 10, MaxInputBytes: 64 << 10,
			DefaultOutputBytes: 256 << 10, MaxOutputBytes: 1 << 20,
			DefaultTimeoutMS: 5_000, MaxTimeoutMS: 10_000,
			DefaultMemoryMB: 128, MaxMemoryMB: 256,
			DefaultCPUMillicores: 250, MaxCPUMillicores: 1000,
			DefaultEphemeralDiskMB: 64, MaxEphemeralDiskMB: 512, PIDsMax: 64,
		},
		PlanPro: {
			Allowed: true, MaxConcurrent: 5, MaxSourceBytes: 256 << 10, MaxInputBytes: 256 << 10,
			DefaultOutputBytes: 256 << 10, MaxOutputBytes: 4 << 20,
			DefaultTimeoutMS: 5_000, MaxTimeoutMS: 30_000,
			DefaultMemoryMB: 128, MaxMemoryMB: 512,
			DefaultCPUMillicores: 250, MaxCPUMillicores: 1000,
			DefaultEphemeralDiskMB: 64, MaxEphemeralDiskMB: 1024, PIDsMax: 64,
		},
		PlanScale: {
			Allowed: true, MaxConcurrent: 20, MaxSourceBytes: 1 << 20, MaxInputBytes: 1 << 20,
			DefaultOutputBytes: 256 << 10, MaxOutputBytes: 16 << 20,
			DefaultTimeoutMS: 5_000, MaxTimeoutMS: 30_000,
			DefaultMemoryMB: 128, MaxMemoryMB: 1024,
			DefaultCPUMillicores: 250, MaxCPUMillicores: 1000,
			DefaultEphemeralDiskMB: 64, MaxEphemeralDiskMB: 2048, PIDsMax: 64,
		},
	}

	for plan, expected := range want {
		got, ok := plan.ExecutionLimits()
		if !ok {
			t.Fatalf("%s.ExecutionLimits() returned ok=false", plan)
		}
		if got != expected {
			t.Errorf("%s.ExecutionLimits() = %+v, want %+v", plan, got, expected)
		}
		if got.Allowed != plan.ExecutionsAllowed() {
			t.Errorf("%s executions gate disagrees with envelope", plan)
		}
	}

	if _, ok := Plan("enterprise").ExecutionLimits(); ok {
		t.Fatal("unknown plan returned ok=true")
	}
	if Plan("enterprise").ExecutionsAllowed() {
		t.Fatal("unknown plan must fail closed")
	}
}

func TestCreateExecutionRequestResolveDefaults(t *testing.T) {
	req := CreateExecutionRequest{
		Runtime: ExecutionRuntimeNode24,
		Source:  "export default async function main(input) { return input }",
	}

	got, problem := req.Resolve(PlanHobby)
	if problem != nil {
		t.Fatalf("Resolve() problem = %+v", problem)
	}
	if got.Runtime != req.Runtime || got.Source != req.Source {
		t.Fatalf("resolved runtime/source = %q/%q", got.Runtime, got.Source)
	}
	if string(got.Input) != "null" {
		t.Errorf("default input = %s, want null", got.Input)
	}
	if got.Network.Mode != ExecutionNetworkNone {
		t.Errorf("default network = %q, want none", got.Network.Mode)
	}
	wantLimits := ResolvedExecutionLimits{
		TimeoutMS: 5_000, MemoryMB: 128, CPUMillicores: 250,
		EphemeralDiskMB: 64, MaxOutputBytes: 256 << 10, PIDsMax: 64,
	}
	if got.Limits != wantLimits {
		t.Errorf("resolved limits = %+v, want %+v", got.Limits, wantLimits)
	}
	if shape := got.SnapshotShape(); shape != (ExecutionSnapshotShape{Runtime: ExecutionRuntimeNode24, MemoryMB: 128, EphemeralDiskMB: 64}) {
		t.Errorf("snapshot shape = %+v", shape)
	}
}

func TestCreateExecutionRequestResolveBundle(t *testing.T) {
	req := CreateExecutionRequest{
		Runtime:    ExecutionRuntimeNode24,
		Entrypoint: "src/main.mjs",
		Files: []ExecutionFile{
			{Path: "src/lib.mjs", Content: []byte("export const answer = 41\n")},
			{Path: "src/main.mjs", Content: []byte("import {answer} from './lib.mjs'; export default async () => answer + 1\n")},
		},
		Input: json.RawMessage(`null`),
	}
	got, problem := req.Resolve(PlanHobby)
	if problem != nil {
		t.Fatalf("Resolve() problem = %+v", problem)
	}
	if got.Source != "" || got.Entrypoint != req.Entrypoint || got.SourceBytes() != 97 {
		t.Fatalf("resolved bundle = %#v (source bytes %d)", got, got.SourceBytes())
	}
	req.Files[0].Content[0] = 'X'
	if got.Files[0].Content[0] == 'X' {
		t.Fatal("resolved bundle aliases request content")
	}
}

func TestCreateExecutionRequestResolveRejectsUnsafeBundle(t *testing.T) {
	base := func() CreateExecutionRequest {
		return CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Entrypoint: "main.mjs", Files: []ExecutionFile{{Path: "main.mjs", Content: []byte("export default () => 1")}}}
	}
	for name, mutate := range map[string]func(*CreateExecutionRequest){
		"traversal": func(r *CreateExecutionRequest) { r.Files[0].Path = "../secret" },
		"absolute":  func(r *CreateExecutionRequest) { r.Files[0].Path = "/tmp/secret" },
		"duplicate": func(r *CreateExecutionRequest) { r.Files = append(r.Files, r.Files[0]) },
		"path conflict": func(r *CreateExecutionRequest) {
			r.Files = append(r.Files, ExecutionFile{Path: "main.mjs/child", Content: []byte("x")})
		},
		"missing entrypoint": func(r *CreateExecutionRequest) { r.Entrypoint = "missing.mjs" },
		"mixed source":       func(r *CreateExecutionRequest) { r.Source = "export default () => 1" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base()
			mutate(&req)
			if _, problem := req.Resolve(PlanHobby); problem == nil || problem.Code != CodeExecutionSourceInvalid {
				t.Fatalf("problem = %+v, want source_invalid", problem)
			}
		})
	}
}

func TestCreateExecutionRequestResolveExplicitEnvelope(t *testing.T) {
	input := json.RawMessage(`{"items":[1,2,3]}`)
	req := CreateExecutionRequest{
		Runtime: ExecutionRuntimePython313,
		Source:  "def main(input, context):\n    return input",
		Input:   input,
		Limits: &ExecutionLimitRequest{
			TimeoutMS: 20_000, MemoryMB: 512, CPUMillicores: 1000,
			EphemeralDiskMB: 1024, MaxOutputBytes: 2 << 20,
		},
		Network: &ExecutionNetworkPolicy{Mode: ExecutionNetworkNone},
	}

	got, problem := req.Resolve(PlanPro)
	if problem != nil {
		t.Fatalf("Resolve() problem = %+v", problem)
	}
	input[0] = '['
	if string(got.Input) != `{"items":[1,2,3]}` {
		t.Fatalf("resolved input aliases request: %s", got.Input)
	}
	want := ResolvedExecutionLimits{
		TimeoutMS: 20_000, MemoryMB: 512, CPUMillicores: 1000,
		EphemeralDiskMB: 1024, MaxOutputBytes: 2 << 20, PIDsMax: 64,
	}
	if got.Limits != want {
		t.Errorf("resolved limits = %+v, want %+v", got.Limits, want)
	}
}

func TestCreateExecutionRequestResolveRejectsInvalidRequests(t *testing.T) {
	valid := func() CreateExecutionRequest {
		return CreateExecutionRequest{
			Runtime: ExecutionRuntimeNode22,
			Source:  "export default async function main() { return 1 }",
			Input:   json.RawMessage(`{"ok":true}`),
		}
	}
	withLimits := func(limits ExecutionLimitRequest) CreateExecutionRequest {
		req := valid()
		req.Limits = &limits
		return req
	}

	tests := []struct {
		name       string
		plan       Plan
		request    CreateExecutionRequest
		wantCode   string
		wantStatus int
	}{
		{name: "free plan", plan: PlanFree, request: valid(), wantCode: CodeExecutionsNotAllowed, wantStatus: http.StatusForbidden},
		{name: "unknown plan", plan: Plan("unknown"), request: valid(), wantCode: CodeExecutionsNotAllowed, wantStatus: http.StatusForbidden},
		{name: "runtime", plan: PlanHobby, request: func() CreateExecutionRequest { r := valid(); r.Runtime = "go124"; return r }(), wantCode: CodeExecutionRuntimeInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "blank source", plan: PlanHobby, request: func() CreateExecutionRequest { r := valid(); r.Source = " \n\t"; return r }(), wantCode: CodeExecutionSourceInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "source nul", plan: PlanHobby, request: func() CreateExecutionRequest { r := valid(); r.Source += "\x00"; return r }(), wantCode: CodeExecutionSourceInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "source cap", plan: PlanHobby, request: func() CreateExecutionRequest { r := valid(); r.Source = strings.Repeat("x", (64<<10)+1); return r }(), wantCode: CodeExecutionPayloadTooLarge, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "input cap", plan: PlanHobby, request: func() CreateExecutionRequest {
			r := valid()
			r.Input = json.RawMessage(`"` + strings.Repeat("x", 64<<10) + `"`)
			return r
		}(), wantCode: CodeExecutionPayloadTooLarge, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "input json", plan: PlanHobby, request: func() CreateExecutionRequest { r := valid(); r.Input = json.RawMessage(`{"broken"`); return r }(), wantCode: CodeExecutionPayloadInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "network", plan: PlanHobby, request: func() CreateExecutionRequest {
			r := valid()
			r.Network = &ExecutionNetworkPolicy{Mode: "public"}
			return r
		}(), wantCode: CodeExecutionNetworkInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "negative", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{MemoryMB: -1}), wantCode: CodeExecutionLimitInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "timeout minimum", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{TimeoutMS: 99}), wantCode: CodeExecutionLimitInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "timeout plan cap", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{TimeoutMS: 10_001}), wantCode: CodeExecutionLimitExceeded, wantStatus: http.StatusForbidden},
		{name: "memory shape", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{MemoryMB: 192}), wantCode: CodeExecutionLimitInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "memory plan cap", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{MemoryMB: 512}), wantCode: CodeExecutionLimitExceeded, wantStatus: http.StatusForbidden},
		{name: "cpu shape", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{CPUMillicores: 750}), wantCode: CodeExecutionLimitInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "disk shape", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{EphemeralDiskMB: 96}), wantCode: CodeExecutionLimitInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "disk plan cap", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{EphemeralDiskMB: 1024}), wantCode: CodeExecutionLimitExceeded, wantStatus: http.StatusForbidden},
		{name: "output minimum", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{MaxOutputBytes: 1000}), wantCode: CodeExecutionLimitInvalid, wantStatus: http.StatusUnprocessableEntity},
		{name: "output plan cap", plan: PlanHobby, request: withLimits(ExecutionLimitRequest{MaxOutputBytes: (1 << 20) + 1}), wantCode: CodeExecutionLimitExceeded, wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, problem := tc.request.Resolve(tc.plan)
			if problem == nil {
				t.Fatal("Resolve() problem = nil")
			}
			if problem.Code != tc.wantCode || problem.Status != tc.wantStatus {
				t.Fatalf("problem = %+v, want code/status %s/%d", problem, tc.wantCode, tc.wantStatus)
			}
			if got := StatusForCode(problem.Code); got != tc.wantStatus {
				t.Errorf("StatusForCode(%q) = %d, want %d", problem.Code, got, tc.wantStatus)
			}
		})
	}
}

func TestExecutionStatusTerminal(t *testing.T) {
	for _, status := range []ExecutionStatus{ExecutionStatusQueued, ExecutionStatusRestoring, ExecutionStatusRunning} {
		if status.Terminal() {
			t.Errorf("%q reported terminal", status)
		}
	}
	for _, status := range []ExecutionStatus{
		ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusTimedOut,
		ExecutionStatusOutOfMemory, ExecutionStatusCancelled,
	} {
		if !status.Terminal() {
			t.Errorf("%q reported non-terminal", status)
		}
	}
}

func TestExecutionResponseNeverCarriesSourceOrInput(t *testing.T) {
	encoded, err := json.Marshal(ExecutionResponse{
		ID:      "exec-1",
		Status:  ExecutionStatusSucceeded,
		Runtime: ExecutionRuntimeNode22,
		Limits: ResolvedExecutionLimits{
			TimeoutMS: 5_000, MemoryMB: 128, CPUMillicores: 250,
			EphemeralDiskMB: 64, MaxOutputBytes: 1024, PIDsMax: 64,
		},
		Result:    json.RawMessage(`{"ok":true}`),
		CreatedAt: "2026-09-09T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, forbidden := range []string{"source", "input"} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("response contains forbidden %q field: %s", forbidden, encoded)
		}
	}
}
