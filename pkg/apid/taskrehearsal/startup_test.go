package taskrehearsal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

type claimFrames []ClaimLogFrame

func (f *claimFrames) Recv() (ClaimLogFrame, error) {
	if len(*f) == 0 {
		return ClaimLogFrame{}, io.EOF
	}
	frame := (*f)[0]
	*f = (*f)[1:]
	return frame, nil
}

type claimInstances struct {
	instance state.Instance
}

func (s *claimInstances) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	if id != s.instance.ID {
		return state.Instance{}, state.ErrNotFound
	}
	return s.instance, nil
}

func (*claimInstances) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, state.ErrNotFound
}

func (*claimInstances) ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error) {
	return state.ScenarioTestMember{}, state.ErrNotFound
}

func (*claimInstances) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	return nil, state.ErrNotFound
}

func TestStartupClaimRequiresLiveLeaseOnPinnedInstance(t *testing.T) {
	since := time.Now()
	scope := tasktoolrehearsal.SmokeScope{
		Namespace: "rehearsal-" + uuid.NewString(), WorkerAppID: uuid.NewString(), WorkerDeploymentID: uuid.NewString(),
	}
	instance := state.Instance{
		ID: uuid.NewString(), AppID: scope.WorkerAppID, DeploymentID: scope.WorkerDeploymentID,
		State: string(state.StateRunning), Mode: string(state.InstanceModeWorker),
		NodeID: uuid.NewString(), WakeID: uuid.NewString(), Netns: "fixture", StartedAt: since.Add(-time.Second),
	}
	receipt := claimReceipt{Event: claimEvent, Namespace: scope.Namespace, TaskID: uuid.NewString(), Attempt: 1,
		WorkerID: uuid.NewString(), LeaseSHA256: strings.Repeat("a", 64)}
	line, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	frame := ClaimLogFrame{InstanceID: instance.ID, DeploymentID: instance.DeploymentID,
		Stream: "stdout", WrittenAt: since.Add(time.Millisecond), Line: string(line)}
	for _, test := range []struct {
		name  string
		drift bool
		live  bool
		want  bool
	}{
		{"exact", false, true, true},
		{"lease no longer live", false, false, false},
		{"wake changed during observation", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &claimInstances{instance: instance}
			d := &NativeStartup{Store: store, NodeID: instance.NodeID}
			wrong := frame
			wrong.DeploymentID = uuid.NewString()
			stale := frame
			stale.WrittenAt = since.Add(-time.Second)
			logs := claimFrames{wrong, stale, frame}
			calls := 0
			got, err := d.observeClaim(t.Context(), scope, logs.Recv, func(_ context.Context, claim pgruntime.TaskClaim) (bool, error) {
				calls++
				if claim.TaskID != receipt.TaskID || claim.WorkerID != receipt.WorkerID ||
					claim.LeaseSHA256 != receipt.LeaseSHA256 || claim.DeploymentID != instance.DeploymentID || claim.Attempt != 1 {
					t.Fatalf("observer received a different claim: %+v", claim)
				}
				if test.drift {
					store.instance.WakeID = uuid.NewString()
				}
				return test.live, nil
			}, receipt.TaskID, since)
			if calls != 1 || (err == nil) != test.want || test.want && got.InstanceID != instance.ID {
				t.Fatalf("claim = %+v, calls = %d, err = %v", got, calls, err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	d := &NativeStartup{}
	if _, err := d.observeClaim(cancelled, scope, nil, nil, receipt.TaskID, since); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read returned %v", err)
	}
}

type startupTransportFunc func(*http.Request) (*http.Response, error)

func (f startupTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStartupSubmitsExactlyOneDurableTask(t *testing.T) {
	taskID := uuid.NewString()
	calls := []string{}
	transport := startupTransportFunc(func(r *http.Request) (*http.Response, error) {
		defer func() { _ = r.Body.Close() }()
		var request struct {
			ID     int
			Method string
			Params struct {
				Name      string
				Arguments struct {
					Report string
					Steps  int
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" || r.Header.Get("Mcp-Method") != request.Method ||
			r.Header.Get("MCP-Protocol-Version") != "2026-07-28" || r.Header.Get("Authorization") != "" {
			return nil, errors.New("request escaped the pinned MCP protocol")
		}
		calls = append(calls, request.Method)
		var result string
		switch request.Method {
		case "server/discover":
			result = `{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{},"extensions":{"io.modelcontextprotocol/tasks":{}}}}`
		case "tools/call":
			if request.Params.Name != "build_report" || request.Params.Arguments.Report != "hosted-task-rehearsal" || request.Params.Arguments.Steps != 20 {
				return nil, errors.New("unexpected rehearsal arguments")
			}
			result = fmt.Sprintf(`{"resultType":"task","taskId":%q,"status":"working","createdAt":"2026-11-21T12:00:00Z","lastUpdatedAt":"2026-11-21T12:00:00Z","ttlMs":60000}`, taskID)
		default:
			return nil, errors.New("unexpected MCP request")
		}
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, request.ID, result)
		return http.ReadResponse(bufio.NewReader(strings.NewReader(
			fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body))), r)
	})
	got, err := submitReport(t.Context(), transport)
	if err != nil || got != taskID || strings.Join(calls, ",") != "server/discover,tools/call" {
		t.Fatalf("submission = %q, %v, calls %v", got, err, calls)
	}
}
