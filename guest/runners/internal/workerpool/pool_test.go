package workerpool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

type testRequest struct {
	Value string `json:"value"`
}
type testResponse struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

func TestInvokeIfSupportedReusesMarkedWorker(t *testing.T) {
	marker := t.TempDir() + "/handler"
	if err := os.WriteFile(marker, []byte("# "+protocolMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		Executable:  os.Args[0],
		Args:        []string{"-test.run=TestWorkerpoolHelperProcess", "--"},
		Env:         append(os.Environ(), "GO_WANT_WORKERPOOL_HELPER=1"),
		HandlerPath: marker,
	}
	for i, value := range []string{"first", "second"} {
		var got testResponse
		handled, err := InvokeIfSupported(context.Background(), spec, testRequest{Value: value}, &got)
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if !handled {
			t.Fatalf("invoke %d was not handled", i)
		}
		if got.Value != value || got.Count != i+1 {
			t.Fatalf("invoke %d response = %+v", i, got)
		}
	}
}

func TestInvokeIfSupportedLeavesLegacyHandlerAlone(t *testing.T) {
	handler := t.TempDir() + "/handler"
	if err := os.WriteFile(handler, []byte("legacy protocol"), 0o600); err != nil {
		t.Fatal(err)
	}
	handled, err := InvokeIfSupported(context.Background(), Spec{HandlerPath: handler}, testRequest{}, &testResponse{})
	if err != nil || handled {
		t.Fatalf("legacy handler = handled %v, err %v", handled, err)
	}
}

func TestWorkerpoolHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WORKERPOOL_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"__faas_ready":true}`)
	scanner := bufio.NewScanner(os.Stdin)
	count := 0
	for scanner.Scan() {
		var request testRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		count++
		body, _ := json.Marshal(testResponse{Value: request.Value, Count: count})
		_, _ = fmt.Fprintln(os.Stdout, string(body))
	}
	os.Exit(0)
}
