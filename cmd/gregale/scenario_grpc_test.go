package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const testGRPCProto = `syntax = "proto3";
package testgrpc.v1;
message Msg { string text = 1; int32 n = 2; }
service Echo {
  rpc Say(Msg) returns (Msg);
  rpc List(Msg) returns (stream Msg);
  rpc Collect(stream Msg) returns (Msg);
  rpc Chat(stream Msg) returns (stream Msg);
  rpc Fail(Msg) returns (Msg);
}
`

// startTestGRPCServer serves testgrpc.v1.Echo from the compiled proto with
// dynamic messages, and server reflection when withReflection is set.
func startTestGRPCServer(t *testing.T, withReflection bool) (baseURL, manifestDir string) {
	t.Helper()
	manifestDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(manifestDir, "protos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "protos", "echo.proto"), []byte(testGRPCProto), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := compileTestGRPCProtos(context.Background(), manifestDir, &testGRPCConfig{Protos: []string{"protos/echo.proto"}})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := files.FindDescriptorByName("testgrpc.v1.Echo")
	if err != nil {
		t.Fatal(err)
	}
	service := descriptor.(protoreflect.ServiceDescriptor)
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		full, _ := grpc.MethodFromServerStream(stream)
		md := service.Methods().ByName(protoreflect.Name(full[strings.LastIndex(full, "/")+1:]))
		if md == nil {
			return status.Error(codes.Unimplemented, full)
		}
		text := md.Input().Fields().ByName("text")
		reply := func(value string, n int32) *dynamicpb.Message {
			out := dynamicpb.NewMessage(md.Output())
			out.Set(text, protoreflect.ValueOfString(value))
			out.Set(md.Output().Fields().ByName("n"), protoreflect.ValueOfInt32(n))
			return out
		}
		recv := func() (*dynamicpb.Message, error) {
			in := dynamicpb.NewMessage(md.Input())
			return in, stream.RecvMsg(in)
		}
		incoming, _ := metadata.FromIncomingContext(stream.Context())
		_ = stream.SetHeader(metadata.Pairs("x-echo", "yes", "x-tenant", strings.Join(incoming.Get("x-tenant"), ",")))
		stream.SetTrailer(metadata.Pairs("x-trace", "t1"))
		switch md.Name() {
		case "Say":
			in, err := recv()
			if err != nil {
				return err
			}
			return stream.SendMsg(reply("hello "+in.Get(text).String(), 1))
		case "List":
			in, err := recv()
			if err != nil {
				return err
			}
			for i := int32(1); i <= 3; i++ {
				if err := stream.SendMsg(reply(fmt.Sprintf("%s-%d", in.Get(text).String(), i), i)); err != nil {
					return err
				}
			}
			return nil
		case "Collect":
			var parts []string
			for {
				in, err := recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return err
				}
				parts = append(parts, in.Get(text).String())
			}
			return stream.SendMsg(reply(strings.Join(parts, "+"), int32(len(parts))))
		case "Chat":
			for {
				in, err := recv()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := stream.SendMsg(reply("re:"+in.Get(text).String(), 0)); err != nil {
					return err
				}
			}
		default:
			return status.Error(codes.NotFound, "no such thing")
		}
	}))
	if withReflection {
		reflectionpb.RegisterServerReflectionServer(server, reflection.NewServerV1(reflection.ServerOptions{Services: server, DescriptorResolver: files}))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return "http://" + listener.Addr().String(), manifestDir
}

func runTestGRPCSteps(t *testing.T, ctx context.Context, baseURL string, steps []testHTTPRequest) ([]testHTTPRequestEvidence, map[string]string, error) {
	t.Helper()
	captures := map[string]string{}
	evidence, err := runTestHTTPRequestsWithData(ctx, baseURL, "run-1", nil, steps, captures, nil)
	return evidence, captures, err
}

func intPointer(n int) *int { return &n }

func TestGRPCStepsAllCallShapes(t *testing.T) {
	baseURL, dir := startTestGRPCServer(t, false)
	ctx := withTestGRPCScenario(context.Background(), &testGRPCConfig{Protos: []string{"protos/echo.proto"}, Reflection: "never"}, dir)
	steps := []testHTTPRequest{
		{Name: "say", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say", Message: map[string]any{"text": "world"}, Metadata: map[string]string{"x-tenant": "${run.id}"}},
			Expect:  testHTTPExpect{Code: "OK", JSON: map[string]any{"/text": "hello world"}, Headers: map[string]string{"x-echo": "yes", "x-tenant": "run-1"}, Trailers: map[string]string{"x-trace": "t1"}},
			Capture: map[string]string{"greeting": "/text"}},
		{Name: "list", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/List", Message: map[string]any{"text": "${steps.say.greeting}"}},
			Expect: testHTTPExpect{MessageCount: intPointer(3), JSON: map[string]any{"/2/text": "hello world-3", "/0/n": 1}}},
		{Name: "collect", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Collect", Messages: []any{map[string]any{"text": "a"}, map[string]any{"text": "b"}}},
			Expect: testHTTPExpect{JSON: map[string]any{"/text": "a+b", "/n": 2}}},
		{Name: "chat", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Chat", Messages: []any{map[string]any{"text": "hi"}, map[string]any{"text": "bye"}}},
			Expect: testHTTPExpect{MessageCount: intPointer(2), JSON: map[string]any{"/1/text": "re:bye"}}},
		{Name: "fail", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Fail"},
			Expect: testHTTPExpect{Code: "NOT_FOUND", Message: stringPointer("no such thing")}},
	}
	evidence, captures, err := runTestGRPCSteps(t, ctx, baseURL, steps)
	if err != nil {
		t.Fatalf("steps failed: %v (evidence %+v)", err, evidence)
	}
	if captures["greeting"] != "hello world" {
		t.Fatalf("captures = %v", captures)
	}
	if evidence[0].Method != "GRPC" || evidence[0].Path != "testgrpc.v1.Echo/Say" || evidence[4].Status != int(codes.NotFound) {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func stringPointer(s string) *string { return &s }

func TestGRPCStepFailuresAreReported(t *testing.T) {
	baseURL, dir := startTestGRPCServer(t, false)
	ctx := withTestGRPCScenario(context.Background(), &testGRPCConfig{Protos: []string{"protos/echo.proto"}}, dir)
	for _, tc := range []struct {
		name string
		step testHTTPRequest
		want string
	}{
		{"wrong status", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Fail"}}, "expected gRPC status OK, received NotFound: no such thing"},
		{"json mismatch", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say", Message: map[string]any{"text": "x"}}, Expect: testHTTPExpect{JSON: map[string]any{"/text": "nope"}}}, `JSON pointer "/text" did not match`},
		{"trailer mismatch", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say"}, Expect: testHTTPExpect{Trailers: map[string]string{"x-trace": "t2"}}}, `expected trailer x-trace="t2"`},
		{"count mismatch", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/List"}, Expect: testHTTPExpect{MessageCount: intPointer(2)}}, "expected 2 response messages, received 3"},
		{"bad message field", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say", Message: map[string]any{"nope": 1}}}, "does not fit testgrpc.v1.Msg"},
		{"wrong shape", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Chat", Message: map[string]any{}}}, "streams requests; use messages"},
		{"unknown method falls to reflection", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Missing"}}, "server reflection for testgrpc.v1.Echo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runTestGRPCSteps(t, ctx, baseURL, []testHTTPRequest{tc.step})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// Without protos the CLI asks the server: reflection v1 resolves the method.
func TestGRPCStepUsesServerReflection(t *testing.T) {
	baseURL, dir := startTestGRPCServer(t, true)
	ctx := withTestGRPCScenario(context.Background(), &testGRPCConfig{Reflection: "always"}, dir)
	_, _, err := runTestGRPCSteps(t, ctx, baseURL, []testHTTPRequest{
		{Name: "say", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say", Message: map[string]any{"text": "r"}}, Expect: testHTTPExpect{JSON: map[string]any{"/text": "hello r"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The YAML spelling from the ADR decodes and validates through the manifest
// reader, protos compiled relative to the manifest.
func TestGRPCStepsFromManifestYAML(t *testing.T) {
	_, dir := startTestGRPCServer(t, false)
	manifest := filepath.Join(dir, "gregale-test.yaml")
	body := `version: 1
scenarios:
  echo-smoke:
    project: shop
    source: .
    grpc:
      protos: [protos/echo.proto]
      reflection: never
    requests:
      - name: say
        grpc:
          method: testgrpc.v1.Echo/Say
          message: {text: world}
          metadata: {x-tenant: acme}
          timeout: 5s
        expect:
          code: OK
          json: {/text: hello world}
          headers: {x-echo: "yes"}
          trailers: {x-trace: t1}
        capture: {greeting: /text}
      - name: chat
        grpc:
          method: testgrpc.v1.Echo/Chat
          messages: [{text: hi}, {text: bye}]
        expect:
          message_count: 2
          json: {/1/text: "re:bye"}
`
	if err := os.WriteFile(manifest, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := readTestManifest(manifest)
	if err != nil {
		t.Fatalf("manifest refused: %v", err)
	}
	say := scenarios["echo-smoke"].Requests[0]
	if say.GRPC == nil || say.GRPC.Method != "testgrpc.v1.Echo/Say" || say.Expect.Trailers["x-trace"] != "t1" {
		t.Fatalf("decoded step = %+v", say)
	}
	broken := strings.Replace(body, "testgrpc.v1.Echo/Chat", "testgrpc.v1.Echo/Nope", 1)
	if err := os.WriteFile(manifest, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios, dir2, err := readTestManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTestGRPCDefinitions(context.Background(), dir2, scenarios["echo-smoke"]); err == nil || !strings.Contains(err.Error(), "the protos do not define testgrpc.v1.Echo/Nope") {
		t.Fatalf("undefined method err = %v", err)
	}
}

func TestValidateGRPCSteps(t *testing.T) {
	_, dir := startTestGRPCServer(t, false)
	base := testScenario{GRPC: &testGRPCConfig{Protos: []string{"protos/echo.proto"}, Reflection: "never"}}
	ok := base
	ok.Requests = []testHTTPRequest{{Name: "say", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say", Message: map[string]any{}}, Expect: testHTTPExpect{Code: 0}}}
	if err := validateTestHTTPRequests(ok); err != nil {
		t.Fatalf("valid step refused: %v", err)
	}
	if err := validateTestGRPCDefinitions(context.Background(), dir, ok); err != nil {
		t.Fatalf("valid definitions refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		step testHTTPRequest
		defs bool
		want string
	}{
		{"http fields", testHTTPRequest{Name: "s", Method: "GET", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Say"}}, false, "cannot also set method"},
		{"method shape", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "Say"}}, false, "package.Service/Method"},
		{"reserved metadata", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "a.B/C", Metadata: map[string]string{"grpc-timeout": "1S"}}}, false, "set by the gRPC client"},
		{"bad code", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "a.B/C"}, Expect: testHTTPExpect{Code: "MAYBE"}}, false, "not a gRPC status"},
		{"long timeout", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "a.B/C", Timeout: "1h"}}, false, "timeout must be"},
		{"undefined method", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Nope"}}, true, "the protos do not define"},
		{"streaming shape", testHTTPRequest{Name: "s", GRPC: &testGRPCCall{Method: "testgrpc.v1.Echo/Collect", Message: map[string]any{}}}, true, "use messages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := base
			scenario.Requests = []testHTTPRequest{tc.step}
			var err error
			if tc.defs {
				err = validateTestGRPCDefinitions(context.Background(), dir, scenario)
			} else {
				err = validateTestHTTPRequests(scenario)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	load := ok
	load.Load = &testLoadSpec{}
	if err := validateTestGRPCLoad(load, false); err == nil || !strings.Contains(err.Error(), "load mode runs HTTP steps only") {
		t.Fatalf("load err = %v", err)
	}
}
