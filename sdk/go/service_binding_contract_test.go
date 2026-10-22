package faas

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

// These cases are also consumed by Node and Python. The schema snapshot is
// checked against OpenAPI in CI, independently of SDK generation.
type bindingContractSchema struct {
	Ref        string                           `json:"$ref"`
	Properties map[string]bindingContractSchema `json:"properties"`
	Enum       []string                         `json:"enum"`
}

func readBindingContract(t *testing.T, name string, out any) {
	t.Helper()
	data, err := os.ReadFile("../service-binding-contracts/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func bindingContractSchemas(t *testing.T) map[string]bindingContractSchema {
	t.Helper()
	var snapshot struct {
		Schemas map[string]bindingContractSchema `json:"schemas"`
	}
	readBindingContract(t, "schema", &snapshot)
	return snapshot.Schemas
}

func TestServiceBindingContractDeclaredFields(t *testing.T) {
	types := map[string]reflect.Type{
		"CreateAppRequest":           reflect.TypeFor[CreateAppRequest](),
		"UpdateAppRequest":           reflect.TypeFor[UpdateAppRequest](),
		"AppResponse":                reflect.TypeFor[AppResponse](),
		"AppManifest":                reflect.TypeFor[AppManifest](),
		"AppServiceBinding":          reflect.TypeFor[AppServiceBinding](),
		"ServiceCallScope":           reflect.TypeFor[ServiceCallScope](),
		"ServiceCallerScopes":        reflect.TypeFor[ServiceCallerScopes](),
		"ServiceReliabilityPolicies": reflect.TypeFor[map[string]ServiceReliabilityPolicy](),
		"ServiceReliabilityPolicy":   reflect.TypeFor[ServiceReliabilityPolicy](),
		"ServiceBindingPolicy":       reflect.TypeFor[ServiceBindingPolicy](),
		"ServiceBindingTransport":    reflect.TypeFor[ServiceBindingTransport](),
	}
	schemas := bindingContractSchemas(t)
	for name, schema := range schemas {
		t.Run(name, func(t *testing.T) {
			typ, ok := types[name]
			if !ok {
				t.Fatalf("add public Go type coverage for OpenAPI schema %s", name)
			}
			fields := map[string]reflect.Type{}
			if typ.Kind() == reflect.Struct {
				for i := 0; i < typ.NumField(); i++ {
					field := typ.Field(i)
					fields[strings.Split(field.Tag.Get("json"), ",")[0]] = field.Type
				}
			}
			for key, prop := range schema.Properties {
				field, ok := fields[key]
				if !ok {
					t.Errorf("%s must declare JSON field %s", name, key)
					continue
				}
				for field.Kind() == reflect.Pointer {
					field = field.Elem()
				}
				if prop.Ref != "" && field != reflect.TypeFor[json.RawMessage]() {
					ref := strings.TrimPrefix(prop.Ref, "#/components/schemas/")
					if field != types[ref] {
						t.Errorf("%s.%s has type %v, want %v", name, key, field, types[ref])
					}
				}
			}
		})
	}
	for name, values := range map[string][]string{
		"ServiceBindingPolicy":    {string(ServiceBindingPolicyAccount), string(ServiceBindingPolicyDeclared)},
		"ServiceBindingTransport": {string(ServiceBindingTransportHTTP), string(ServiceBindingTransportHTTPS)},
	} {
		if !reflect.DeepEqual(schemas[name].Enum, values) {
			t.Errorf("%s constants = %v; OpenAPI = %v", name, values, schemas[name].Enum)
		}
	}
}

func bindingContractProjection(schemas map[string]bindingContractSchema, schema bindingContractSchema, value any) any {
	if value == nil {
		return nil
	}
	if schema.Ref != "" {
		return bindingContractProjection(schemas, schemas[strings.TrimPrefix(schema.Ref, "#/components/schemas/")], value)
	}
	if object, ok := value.(map[string]any); ok && schema.Properties != nil {
		out := map[string]any{}
		for name, prop := range schema.Properties {
			if child, exists := object[name]; exists {
				out[name] = bindingContractProjection(schemas, prop, child)
			}
		}
		return out
	}
	return value
}

func assertBindingContractJSON(t *testing.T, schemas map[string]bindingContractSchema, model string, got, want []byte) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &expected); err != nil {
		t.Fatal(err)
	}
	actual = bindingContractProjection(schemas, schemas[model], actual)
	expected = bindingContractProjection(schemas, schemas[model], expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s binding fields = %#v; want %#v", model, actual, expected)
	}
}

type bindingContractTransport func(*http.Request) (*http.Response, error)

func (f bindingContractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceBindingContractSharedWireCases(t *testing.T) {
	var cases struct {
		Requests []struct {
			Name, Schema string
			Body         json.RawMessage
		}
		Responses []struct {
			Name string
			Body json.RawMessage
		}
	}
	readBindingContract(t, "cases", &cases)
	schemas := bindingContractSchemas(t)
	for _, tc := range cases.Requests {
		t.Run(tc.Name, func(t *testing.T) {
			calls := 0
			client, err := NewClient("https://api.invalid", "fixture", WithHTTPClient(&http.Client{Transport: bindingContractTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				method, path := http.MethodPatch, "/v1/apps/demo"
				if tc.Schema == "CreateAppRequest" {
					method, path = http.MethodPost, "/v1/apps"
				}
				if r.Method != method || r.URL.Path != path {
					t.Fatalf("request = %s %s; want %s %s", r.Method, r.URL.Path, method, path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				assertBindingContractJSON(t, schemas, tc.Schema, body, tc.Body)
				status := http.StatusOK
				if method == http.MethodPost {
					status = http.StatusCreated
				}
				return &http.Response{
					StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(bytes.NewReader(cases.Responses[1].Body)), Request: r,
				}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			var app AppResponse
			switch tc.Schema {
			case "CreateAppRequest":
				var request CreateAppRequest
				if err := json.Unmarshal(tc.Body, &request); err != nil {
					t.Fatal(err)
				}
				app, err = client.CreateApp(context.Background(), request)
			case "UpdateAppRequest":
				var request UpdateAppRequest
				if err := json.Unmarshal(tc.Body, &request); err != nil {
					t.Fatal(err)
				}
				app, err = client.UpdateApp(context.Background(), "demo", request)
			default:
				t.Fatalf("unknown request schema %s", tc.Schema)
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || app.Manifest.ServiceBindingTransport != ServiceBindingTransportHTTPS {
				t.Fatalf("calls=%d; nested transport=%q", calls, app.Manifest.ServiceBindingTransport)
			}
		})
	}
	for _, tc := range cases.Responses {
		t.Run("response/"+tc.Name, func(t *testing.T) {
			client, err := NewClient("https://api.invalid", "fixture", WithHTTPClient(&http.Client{Transport: bindingContractTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo" {
					t.Fatalf("request = %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(bytes.NewReader(tc.Body)), Request: r,
				}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			app, err := client.GetApp(context.Background(), "demo")
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(app)
			if err != nil {
				t.Fatal(err)
			}
			assertBindingContractJSON(t, schemas, "AppResponse", body, tc.Body)
		})
	}
}
