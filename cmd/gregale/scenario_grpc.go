package main

// adr: 20261115110124 — gRPC steps in `gregale test` scenarios.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	testGRPCMaxResponseMessages = 1000
	testGRPCMaxResponseBytes    = 1 << 20
	testGRPCDefaultTimeout      = 30 * time.Second
	testGRPCMaxTimeout          = 5 * time.Minute
)

var testGRPCMethodPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*/[A-Za-z_][A-Za-z0-9_]*$`)
var testGRPCMetadataKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// testGRPCConfig is a scenario's `grpc:` block: where method definitions
// come from.
type testGRPCConfig struct {
	Protos      []string `yaml:"protos,omitempty" json:"protos,omitempty"`
	ImportPaths []string `yaml:"import_paths,omitempty" json:"import_paths,omitempty"`
	// Reflection is auto (default: protos first, then server reflection),
	// always (server reflection only) or never (protos only).
	Reflection string `yaml:"reflection,omitempty" json:"reflection,omitempty"`
}

// testGRPCCall is a step's `grpc:` block.
type testGRPCCall struct {
	Method   string            `yaml:"method" json:"method"`
	Message  any               `yaml:"message,omitempty" json:"message,omitempty"`
	Messages []any             `yaml:"messages,omitempty" json:"messages,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Timeout  string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

func (c *testGRPCConfig) reflectionMode() string {
	if c == nil || c.Reflection == "" {
		return "auto"
	}
	return c.Reflection
}

type testGRPCScenarioKey struct{}
type testGRPCDialTargetKey struct{}

type testGRPCScenarioContext struct {
	config      *testGRPCConfig
	manifestDir string
	once        sync.Once
	files       *protoregistry.Files
	err         error
}

// withTestGRPCScenario carries the scenario's definitions to the step runner,
// which runs without the scenario. Protos compile on the first gRPC step.
func withTestGRPCScenario(ctx context.Context, config *testGRPCConfig, manifestDir string) context.Context {
	return context.WithValue(ctx, testGRPCScenarioKey{}, &testGRPCScenarioContext{config: config, manifestDir: manifestDir})
}

// withTestGRPCDialTarget maps the HTTP/1 evidence proxy's base URL to the app
// URL a gRPC step dials instead.
func withTestGRPCDialTarget(ctx context.Context, proxyURL, appURL string) context.Context {
	targets, _ := ctx.Value(testGRPCDialTargetKey{}).(map[string]string)
	next := make(map[string]string, len(targets)+1)
	for key, value := range targets {
		next[key] = value
	}
	next[proxyURL] = appURL
	return context.WithValue(ctx, testGRPCDialTargetKey{}, next)
}

func (s *testGRPCScenarioContext) protoFiles() (*protoregistry.Files, error) {
	s.once.Do(func() {
		if s.config == nil || len(s.config.Protos) == 0 {
			return
		}
		s.files, s.err = compileTestGRPCProtos(s.manifestDir, s.config)
	})
	return s.files, s.err
}

// compileTestGRPCProtos compiles the scenario's proto files. Without
// import_paths each file's own directory is its import path.
func compileTestGRPCProtos(manifestDir string, config *testGRPCConfig) (*protoregistry.Files, error) {
	importPaths := make([]string, 0, len(config.ImportPaths))
	for _, dir := range config.ImportPaths {
		importPaths = append(importPaths, filepath.Join(manifestDir, filepath.FromSlash(dir)))
	}
	names := make([]string, 0, len(config.Protos))
	for _, proto := range config.Protos {
		abs := filepath.Join(manifestDir, filepath.FromSlash(proto))
		if info, err := os.Stat(abs); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("proto %s: not a readable file", proto)
		}
		name := ""
		for _, dir := range importPaths {
			if rel, err := filepath.Rel(dir, abs); err == nil && !strings.HasPrefix(rel, "..") {
				name = filepath.ToSlash(rel)
				break
			}
		}
		if name == "" {
			if len(config.ImportPaths) > 0 {
				return nil, fmt.Errorf("proto %s is outside every import path", proto)
			}
			importPaths = append(importPaths, filepath.Dir(abs))
			name = filepath.Base(abs)
		}
		names = append(names, name)
	}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: importPaths})}
	compiled, err := compiler.Compile(context.Background(), names...)
	if err != nil {
		return nil, fmt.Errorf("compile protos: %w", err)
	}
	files := new(protoregistry.Files)
	var register func(protoreflect.FileDescriptor) error
	register = func(fd protoreflect.FileDescriptor) error {
		if _, err := files.FindFileByPath(fd.Path()); err == nil {
			return nil
		}
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			if err := register(imports.Get(i).FileDescriptor); err != nil {
				return err
			}
		}
		return files.RegisterFile(fd)
	}
	for _, fd := range compiled {
		if err := register(fd); err != nil {
			return nil, fmt.Errorf("register protos: %w", err)
		}
	}
	return files, nil
}

func findTestGRPCMethod(files interface {
	FindDescriptorByName(protoreflect.FullName) (protoreflect.Descriptor, error)
}, method string) (protoreflect.MethodDescriptor, bool) {
	if files == nil {
		return nil, false
	}
	service, name, _ := strings.Cut(method, "/")
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, false
	}
	sd, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, false
	}
	md := sd.Methods().ByName(protoreflect.Name(name))
	return md, md != nil
}

// validateTestGRPCStep checks a gRPC step offline: method shape, message
// fields for that shape when the protos define it, metadata, timeout and
// expectations.
func validateTestGRPCStep(scenario testScenario, step testHTTPRequest, known map[string]string) error {
	call := step.GRPC
	if step.Method != "" || step.Path != "" || len(step.Headers) > 0 || step.hasJSONBody() || step.Expect.Status != 0 || step.Expect.ContentType != "" {
		return errors.New("a gRPC step cannot also set method, path, headers, json, status or content_type")
	}
	if !testGRPCMethodPattern.MatchString(call.Method) {
		return fmt.Errorf("method %q must be package.Service/Method", call.Method)
	}
	mode := scenario.GRPC.reflectionMode()
	if mode != "auto" && mode != "always" && mode != "never" {
		return fmt.Errorf("grpc.reflection must be auto, always or never")
	}
	if call.Message != nil && len(call.Messages) > 0 {
		return errors.New("set message or messages, not both")
	}
	if err := validateTestHTTPJSON(call.Message, known); err != nil {
		return fmt.Errorf("message: %w", err)
	}
	for i, message := range call.Messages {
		if err := validateTestHTTPJSON(message, known); err != nil {
			return fmt.Errorf("messages[%d]: %w", i, err)
		}
	}
	if call.Timeout != "" {
		timeout, err := time.ParseDuration(call.Timeout)
		if err != nil || timeout <= 0 || timeout > testGRPCMaxTimeout {
			return fmt.Errorf("timeout must be a duration up to %s", testGRPCMaxTimeout)
		}
	}
	for key, value := range call.Metadata {
		if err := validateTestGRPCMetadataKey(key, step.As != ""); err != nil {
			return err
		}
		if _, err := expandTestHTTPTemplate(value, known, nil); err != nil {
			return fmt.Errorf("metadata %s: %w", key, err)
		}
	}
	if _, err := testGRPCExpectedCode(step.Expect.Code); err != nil {
		return err
	}
	if step.Expect.MessageCount != nil && *step.Expect.MessageCount < 0 {
		return errors.New("message_count must not be negative")
	}
	for pointer := range step.Expect.JSON {
		if _, err := testJSONPointerParts(pointer); err != nil {
			return fmt.Errorf("json %q: %w", pointer, err)
		}
	}
	for _, group := range []map[string]string{step.Expect.Headers, step.Expect.Trailers} {
		for key, value := range group {
			if !testGRPCMetadataKeyPattern.MatchString(key) {
				return fmt.Errorf("metadata key %q must be lower case", key)
			}
			if _, err := expandTestHTTPTemplate(value, known, nil); err != nil {
				return fmt.Errorf("expected metadata %s: %w", key, err)
			}
		}
	}
	if scenario.GRPC == nil || len(scenario.GRPC.Protos) == 0 || mode == "always" {
		if mode == "never" {
			return errors.New("grpc.reflection: never needs grpc.protos")
		}
		return nil
	}
	return nil
}

// validateTestGRPCDefinitions compiles a scenario's protos and checks each
// gRPC step's method and message shape against them. Steps whose method the
// protos do not define are left to server reflection unless it is off.
func validateTestGRPCDefinitions(manifestDir string, scenario testScenario) error {
	steps := testScenarioAllHTTPSteps(scenario)
	hasGRPC := false
	for _, step := range steps {
		hasGRPC = hasGRPC || step.GRPC != nil
	}
	if !hasGRPC || scenario.GRPC == nil || len(scenario.GRPC.Protos) == 0 || scenario.GRPC.reflectionMode() == "always" {
		return nil
	}
	files, err := compileTestGRPCProtos(manifestDir, scenario.GRPC)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.GRPC == nil {
			continue
		}
		md, ok := findTestGRPCMethod(files, step.GRPC.Method)
		if !ok {
			if scenario.GRPC.reflectionMode() == "never" {
				return fmt.Errorf("gRPC step %q: the protos do not define %s", step.Name, step.GRPC.Method)
			}
			continue
		}
		if err := checkTestGRPCShape(md, step.GRPC); err != nil {
			return fmt.Errorf("gRPC step %q: %w", step.Name, err)
		}
	}
	return nil
}

// validateTestGRPCLoad refuses gRPC steps where load mode would run them:
// the load engine sends HTTP requests only. loadFlag is `--load`.
func validateTestGRPCLoad(scenario testScenario, loadFlag bool) error {
	for _, staged := range scenario.Steps {
		if staged.Load == nil {
			continue
		}
		for _, step := range append(append([]testHTTPRequest{}, staged.Requests...), staged.Checks...) {
			if step.GRPC != nil {
				return fmt.Errorf("staged step %q: load mode runs HTTP steps only; gRPC step %q cannot be load-tested", staged.Name, step.Name)
			}
		}
	}
	if !loadFlag && scenario.Load == nil {
		return nil
	}
	for _, step := range append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...) {
		if step.GRPC != nil {
			return fmt.Errorf("load mode runs HTTP steps only; gRPC step %q cannot be load-tested", step.Name)
		}
	}
	return nil
}

func testScenarioAllHTTPSteps(scenario testScenario) []testHTTPRequest {
	steps := append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...)
	for _, staged := range scenario.Steps {
		steps = append(append(steps, staged.Requests...), staged.Checks...)
	}
	return steps
}

func checkTestGRPCShape(md protoreflect.MethodDescriptor, call *testGRPCCall) error {
	if md.IsStreamingClient() && call.Message != nil {
		return fmt.Errorf("%s streams requests; use messages", call.Method)
	}
	if !md.IsStreamingClient() && len(call.Messages) > 0 {
		return fmt.Errorf("%s takes one request; use message", call.Method)
	}
	return nil
}

func validateTestGRPCMetadataKey(key string, hasConsumer bool) error {
	switch {
	case !testGRPCMetadataKeyPattern.MatchString(key):
		return fmt.Errorf("metadata key %q must be lower case letters, digits, '.', '_' or '-'", key)
	case strings.HasPrefix(key, "grpc-"), key == "content-type", key == "te", key == "user-agent", key == "host":
		return fmt.Errorf("metadata key %q is set by the gRPC client", key)
	case hasConsumer && key == "authorization":
		return errors.New("metadata cannot set authorization with as")
	}
	return nil
}

func testGRPCExpectedCode(raw any) (codes.Code, error) {
	switch value := raw.(type) {
	case nil:
		return codes.OK, nil
	case string:
		var code codes.Code
		if err := code.UnmarshalJSON([]byte(strconv.Quote(strings.ToUpper(strings.TrimSpace(value))))); err != nil {
			return 0, fmt.Errorf("code %q is not a gRPC status", value)
		}
		return code, nil
	case int:
		return testGRPCCodeNumber(int64(value))
	case int64:
		return testGRPCCodeNumber(value)
	case uint64:
		return testGRPCCodeNumber(int64(value))
	case float64:
		if value != float64(int64(value)) {
			return 0, fmt.Errorf("code %v is not a gRPC status", value)
		}
		return testGRPCCodeNumber(int64(value))
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0, fmt.Errorf("code %v is not a gRPC status", value)
		}
		return testGRPCCodeNumber(n)
	}
	return 0, fmt.Errorf("code %v is not a gRPC status", raw)
}

func testGRPCCodeNumber(n int64) (codes.Code, error) {
	if n < 0 || n > 16 {
		return 0, fmt.Errorf("code %d is not a gRPC status", n)
	}
	return codes.Code(n), nil
}

// runOneTestGRPCRequest makes one gRPC call and checks its expectations.
func runOneTestGRPCRequest(ctx context.Context, baseURL string, step testHTTPRequest, values, consumerKeys, captures map[string]string, data map[string]any, evidence *testHTTPRequestEvidence) error {
	call := step.GRPC
	scenario, _ := ctx.Value(testGRPCScenarioKey{}).(*testGRPCScenarioContext)
	if scenario == nil {
		scenario = &testGRPCScenarioContext{}
	}
	if targets, ok := ctx.Value(testGRPCDialTargetKey{}).(map[string]string); ok && targets[baseURL] != "" {
		baseURL = targets[baseURL]
	}
	conn, err := dialTestGRPC(baseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	timeout := testGRPCDefaultTimeout
	if call.Timeout != "" {
		if parsed, err := time.ParseDuration(call.Timeout); err == nil && parsed > 0 && parsed <= testGRPCMaxTimeout {
			timeout = parsed
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	md, err := resolveTestGRPCMethod(callCtx, scenario, conn, call.Method)
	if err != nil {
		return err
	}
	if err := checkTestGRPCShape(md, call); err != nil {
		return err
	}
	requests, err := testGRPCRequestMessages(md, call, values, data)
	if err != nil {
		return err
	}
	outgoing := metadata.MD{}
	for key, value := range call.Metadata {
		expanded, err := expandTestHTTPTemplate(value, values, nil)
		if err != nil {
			return fmt.Errorf("metadata %s: %w", key, err)
		}
		outgoing.Append(key, expanded)
	}
	if step.As != "" {
		token := testHTTPConsumerKey(consumerKeys, step.As)
		if token == "" {
			return fmt.Errorf("consumer %q has no test key", step.As)
		}
		outgoing.Set("authorization", "Bearer "+token)
	}
	if len(outgoing) > 0 {
		callCtx = metadata.NewOutgoingContext(callCtx, outgoing)
	}
	responses, header, trailer, callErr := invokeTestGRPC(callCtx, conn, md, requests)
	got := status.Convert(callErr)
	evidence.Status = int(got.Code())
	want, err := testGRPCExpectedCode(step.Expect.Code)
	if err != nil {
		return err
	}
	if got.Code() != want {
		detail := ""
		if got.Message() != "" {
			detail = ": " + got.Message()
		}
		return fmt.Errorf("expected gRPC status %s, received %s%s", want, got.Code(), detail)
	}
	if step.Expect.Message != nil && got.Message() != *step.Expect.Message {
		return fmt.Errorf("expected status message %q, received %q", *step.Expect.Message, got.Message())
	}
	if err := checkTestGRPCMetadata("header", header, step.Expect.Headers, values); err != nil {
		return err
	}
	if err := checkTestGRPCMetadata("trailer", trailer, step.Expect.Trailers, values); err != nil {
		return err
	}
	if step.Expect.MessageCount != nil && len(responses) != *step.Expect.MessageCount {
		return fmt.Errorf("expected %d response messages, received %d", *step.Expect.MessageCount, len(responses))
	}
	if len(step.Expect.JSON) == 0 && len(step.Capture) == 0 {
		return nil
	}
	document, err := testGRPCDocument(md, responses)
	if err != nil {
		return err
	}
	return checkTestJSONDocument(document, step, values, captures, data)
}

// dialTestGRPC connects to a scenario base URL: cleartext HTTP/2 for http://
// (the local engine's loopback app), TLS for https:// (a deployed app).
func dialTestGRPC(baseURL string) (*grpc.ClientConn, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("gRPC target %q is not a URL", baseURL)
	}
	var creds credentials.TransportCredentials
	port := parsed.Port()
	switch parsed.Scheme {
	case "http":
		creds = insecure.NewCredentials()
		if port == "" {
			port = "80"
		}
	case "https":
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname()})
		if port == "" {
			port = "443"
		}
	default:
		return nil, fmt.Errorf("gRPC target %q must be http or https", baseURL)
	}
	conn, err := grpc.NewClient(net.JoinHostPort(parsed.Hostname(), port), grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", baseURL, err)
	}
	return conn, nil
}

func resolveTestGRPCMethod(ctx context.Context, scenario *testGRPCScenarioContext, conn *grpc.ClientConn, method string) (protoreflect.MethodDescriptor, error) {
	mode := scenario.config.reflectionMode()
	if mode != "always" {
		files, err := scenario.protoFiles()
		if err != nil {
			return nil, err
		}
		if md, ok := findTestGRPCMethod(files, method); ok {
			return md, nil
		}
		if mode == "never" {
			return nil, fmt.Errorf("the protos do not define %s", method)
		}
	}
	service, _, _ := strings.Cut(method, "/")
	files, err := reflectTestGRPCFiles(ctx, conn, service)
	if err != nil {
		return nil, fmt.Errorf("server reflection for %s: %w (add the service's .proto to grpc.protos)", service, err)
	}
	md, ok := findTestGRPCMethod(files, method)
	if !ok {
		return nil, fmt.Errorf("the server does not define %s", method)
	}
	return md, nil
}

// testGRPCReflectionExchange asks the server for files: one containing a
// symbol, or one by name.
type testGRPCReflectionExchange func(symbol, filename string) ([][]byte, error)

// reflectTestGRPCFiles loads the file defining service, and its imports,
// through server reflection (v1, then v1alpha).
func reflectTestGRPCFiles(ctx context.Context, conn *grpc.ClientConn, service string) (*protoregistry.Files, error) {
	exchange, closeExchange, err := openTestGRPCReflection(ctx, conn)
	if err != nil {
		return nil, err
	}
	defer closeExchange()
	byName := map[string]*descriptorpb.FileDescriptorProto{}
	add := func(raw [][]byte) error {
		for _, encoded := range raw {
			fd := new(descriptorpb.FileDescriptorProto)
			if err := proto.Unmarshal(encoded, fd); err != nil {
				return fmt.Errorf("decode descriptor: %w", err)
			}
			byName[fd.GetName()] = fd
		}
		return nil
	}
	raw, err := exchange(service, "")
	if err != nil {
		return nil, err
	}
	if err := add(raw); err != nil {
		return nil, err
	}
	for pending := true; pending; {
		pending = false
		for _, fd := range byName {
			for _, dep := range fd.GetDependency() {
				if _, ok := byName[dep]; ok {
					continue
				}
				if known, err := protoregistry.GlobalFiles.FindFileByPath(dep); err == nil {
					byName[dep] = protodesc.ToFileDescriptorProto(known)
					pending = true
					continue
				}
				raw, err := exchange("", dep)
				if err != nil {
					return nil, fmt.Errorf("import %s: %w", dep, err)
				}
				if err := add(raw); err != nil {
					return nil, err
				}
				if _, ok := byName[dep]; !ok {
					return nil, fmt.Errorf("server did not return import %s", dep)
				}
				pending = true
			}
		}
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, fd := range byName {
		set.File = append(set.File, fd)
	}
	return protodesc.NewFiles(set)
}

func openTestGRPCReflection(ctx context.Context, conn *grpc.ClientConn) (testGRPCReflectionExchange, func(), error) {
	v1, err := reflectionv1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err == nil {
		exchange := func(symbol, filename string) ([][]byte, error) {
			request := &reflectionv1.ServerReflectionRequest{}
			if symbol != "" {
				request.MessageRequest = &reflectionv1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol}
			} else {
				request.MessageRequest = &reflectionv1.ServerReflectionRequest_FileByFilename{FileByFilename: filename}
			}
			if err := v1.Send(request); err != nil {
				return nil, err
			}
			response, err := v1.Recv()
			if err != nil {
				return nil, err
			}
			if failure := response.GetErrorResponse(); failure != nil {
				return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
			}
			return response.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
		}
		// A server without v1 answers the first exchange Unimplemented.
		probe := func(symbol, filename string) ([][]byte, error) { return exchange(symbol, filename) }
		return probeTestGRPCReflection(ctx, conn, probe, func() { _ = v1.CloseSend() })
	}
	return openTestGRPCReflectionV1Alpha(ctx, conn)
}

// probeTestGRPCReflection falls back to v1alpha when the v1 service answers
// its first exchange Unimplemented.
func probeTestGRPCReflection(ctx context.Context, conn *grpc.ClientConn, v1 testGRPCReflectionExchange, closeV1 func()) (testGRPCReflectionExchange, func(), error) {
	first := true
	var fallback testGRPCReflectionExchange
	closeFallback := func() {}
	exchange := func(symbol, filename string) ([][]byte, error) {
		if fallback != nil {
			return fallback(symbol, filename)
		}
		raw, err := v1(symbol, filename)
		if first && status.Code(err) == codes.Unimplemented {
			first = false
			var openErr error
			fallback, closeFallback, openErr = openTestGRPCReflectionV1Alpha(ctx, conn)
			if openErr != nil {
				return nil, openErr
			}
			return fallback(symbol, filename)
		}
		first = false
		return raw, err
	}
	return exchange, func() { closeV1(); closeFallback() }, nil
}

func openTestGRPCReflectionV1Alpha(ctx context.Context, conn *grpc.ClientConn) (testGRPCReflectionExchange, func(), error) {
	stream, err := reflectionv1alpha.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, nil, err
	}
	exchange := func(symbol, filename string) ([][]byte, error) {
		request := &reflectionv1alpha.ServerReflectionRequest{}
		if symbol != "" {
			request.MessageRequest = &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: symbol}
		} else {
			request.MessageRequest = &reflectionv1alpha.ServerReflectionRequest_FileByFilename{FileByFilename: filename}
		}
		if err := stream.Send(request); err != nil {
			return nil, err
		}
		response, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		if failure := response.GetErrorResponse(); failure != nil {
			return nil, status.Error(codes.Code(failure.GetErrorCode()), failure.GetErrorMessage())
		}
		return response.GetFileDescriptorResponse().GetFileDescriptorProto(), nil
	}
	return exchange, func() { _ = stream.CloseSend() }, nil
}

// testGRPCRequestMessages expands the step's request JSON and parses it into
// the method's input type.
func testGRPCRequestMessages(md protoreflect.MethodDescriptor, call *testGRPCCall, values map[string]string, data map[string]any) ([]proto.Message, error) {
	raw := call.Messages
	if !md.IsStreamingClient() {
		raw = []any{call.Message}
	}
	out := make([]proto.Message, 0, len(raw))
	for i, value := range raw {
		if value == nil {
			value = map[string]any{}
		}
		expanded, err := expandTestHTTPJSONWithData(value, values, data)
		if err != nil {
			return nil, fmt.Errorf("request message %d: %w", i, err)
		}
		encoded, err := json.Marshal(expanded)
		if err != nil {
			return nil, fmt.Errorf("encode request message %d: %w", i, err)
		}
		if len(encoded) > testHTTPBodyLimit {
			return nil, fmt.Errorf("request message %d exceeds 1 MiB", i)
		}
		message := dynamicpb.NewMessage(md.Input())
		if err := protojson.Unmarshal(encoded, message); err != nil {
			return nil, fmt.Errorf("request message %d does not fit %s: %w", i, md.Input().FullName(), err)
		}
		out = append(out, message)
	}
	return out, nil
}

// invokeTestGRPC runs the call over one stream for every shape: it sends the
// requests, closes the send side, and reads responses until the status.
func invokeTestGRPC(ctx context.Context, conn *grpc.ClientConn, md protoreflect.MethodDescriptor, requests []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error) {
	desc := &grpc.StreamDesc{StreamName: string(md.Name()), ServerStreams: md.IsStreamingServer(), ClientStreams: md.IsStreamingClient()}
	method := "/" + string(md.Parent().FullName()) + "/" + string(md.Name())
	stream, err := conn.NewStream(ctx, desc, method)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, request := range requests {
		if err := stream.SendMsg(request); err != nil {
			if errors.Is(err, io.EOF) {
				break // the server ended the call; RecvMsg reports its status
			}
			return nil, nil, nil, err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, nil, nil, err
	}
	var responses []proto.Message
	var callErr error
	for {
		response := dynamicpb.NewMessage(md.Output())
		err := stream.RecvMsg(response)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			callErr = err
			break
		}
		responses = append(responses, response)
		if len(responses) > testGRPCMaxResponseMessages {
			return nil, nil, nil, fmt.Errorf("more than %d response messages", testGRPCMaxResponseMessages)
		}
		if !md.IsStreamingServer() {
			// A unary response ends with the status; drain it.
			if err := stream.RecvMsg(dynamicpb.NewMessage(md.Output())); err != nil && !errors.Is(err, io.EOF) {
				callErr = err
			}
			break
		}
	}
	header, _ := stream.Header()
	return responses, header, stream.Trailer(), callErr
}

func checkTestGRPCMetadata(kind string, actual metadata.MD, expected map[string]string, values map[string]string) error {
	for key, raw := range expected {
		want, err := expandTestHTTPTemplate(raw, values, nil)
		if err != nil {
			return fmt.Errorf("expected %s %s: %w", kind, key, err)
		}
		found := false
		for _, value := range actual.Get(key) {
			if value == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("expected %s %s=%q, received %q", kind, key, want, actual.Get(key))
		}
	}
	return nil
}

// testGRPCDocument renders responses as the JSON document assertions read:
// the response for unary and client streaming, an array for server and
// bidirectional streaming.
func testGRPCDocument(md protoreflect.MethodDescriptor, responses []proto.Message) (any, error) {
	rendered := make([]json.RawMessage, 0, len(responses))
	total := 0
	for _, response := range responses {
		encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(response)
		if err != nil {
			return nil, fmt.Errorf("render response: %w", err)
		}
		total += len(encoded)
		if total > testGRPCMaxResponseBytes {
			return nil, errors.New("response messages exceed 1 MiB of JSON")
		}
		rendered = append(rendered, encoded)
	}
	var encoded []byte
	if md.IsStreamingServer() {
		var err error
		if encoded, err = json.Marshal(rendered); err != nil {
			return nil, err
		}
	} else {
		if len(rendered) == 0 {
			return nil, errors.New("the call returned no response message")
		}
		encoded = rendered[0]
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode response JSON: %w", err)
	}
	return document, nil
}
