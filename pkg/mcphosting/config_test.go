package mcphosting

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigRequiresExplicitSafeProfile(t *testing.T) {
	valid := Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", Legacy: true, AllowedOrigins: []string{}, Auth: AuthConfig{Mode: "open"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"missing auth", func(c *Config) { c.Auth.Mode = "" }},
		{"stdio", func(c *Config) { c.Transport = "stdio" }},
		{"stateful", func(c *Config) { c.Mode = "stateful" }},
		{"redirect path", func(c *Config) { c.Endpoint = "//evil.example" }},
		{"query", func(c *Config) { c.Endpoint = "/mcp?token=x" }},
		{"control", func(c *Config) { c.Endpoint = "/mcp\t" }},
		{"space", func(c *Config) { c.Endpoint = "/mcp path" }},
		{"route parameter", func(c *Config) { c.Endpoint = "/mcp/:tenant" }},
		{"dot segment", func(c *Config) { c.Endpoint = "/mcp/../healthz" }},
		{"encoded segment", func(c *Config) { c.Endpoint = "/mcp/%2e%2e" }},
		{"bad origin", func(c *Config) { c.AllowedOrigins = []string{"https://example.com/path"} }},
		{"open issuer", func(c *Config) { c.Auth.Issuer = "https://issuer.example" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid profile")
			}
		})
	}
	valid.Auth = AuthConfig{Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	valid.Auth.Resource = "https://app.example/other"
	if err := valid.Validate(); err == nil {
		t.Fatal("accepted different resource endpoint")
	}
	valid.Auth.Resource = "https://app.example/mcp"
	body, _ := json.Marshal(valid)
	for _, suffix := range []string{` {}`, strings.Repeat(" ", 64<<10)} {
		if _, err := Decode(strings.NewReader(string(body) + suffix)); err == nil {
			t.Fatal("accepted oversized or trailing config")
		}
	}
	if _, err := Decode(strings.NewReader(strings.Replace(string(body), `"version":1`, `"version":1,"typo":true`, 1))); err == nil {
		t.Fatal("accepted unknown field")
	}
}

func TestConfigToolScopes(t *testing.T) {
	c := Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{}, Auth: AuthConfig{Mode: "open"}}
	for _, tc := range []struct {
		name, policy string
		valid        bool
	}{
		{"empty denies all", `{}`, true},
		{"explicit public tool", `{"greet":[]}`, true},
		{"null policy", `null`, false},
		{"array policy", `[]`, false},
		{"null scopes", `{"greet":null}`, false},
		{"string scopes", `{"greet":"read"}`, false},
		{"empty name", `{"":[]}`, false},
		{"control name", `{"bad\u0000name":[]}`, false},
		{"open scoped tool", `{"greet":["read"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(c)
			body = []byte(strings.Replace(string(body), `"mode":"open"`, `"mode":"open","tool_scopes":`+tc.policy, 1))
			decoded, err := Decode(strings.NewReader(string(body)))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil {
				return
			}
			encoded, err := json.Marshal(decoded)
			if err != nil || !strings.Contains(string(encoded), `"tool_scopes":`+tc.policy) {
				t.Fatalf("policy lost on serialization: %s err=%v", encoded, err)
			}
		})
	}
	c.Auth = AuthConfig{Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"}}
	for _, scopes := range [][]string{{}, {"files:read", "files:write"}} {
		c.Auth.ToolScopes = ToolScopePolicy{"read": scopes}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, scopes := range [][]string{nil, {""}, {"two words"}, {"quote\""}, {"slash\\"}, {"nonascii:é"}} {
		c.Auth.ToolScopes = ToolScopePolicy{"read": scopes}
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted invalid scopes %q", scopes)
		}
	}
}

func TestConfigResourceAndPromptScopes(t *testing.T) {
	c := Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{}, Auth: AuthConfig{Mode: "open"}}
	for _, tc := range []struct {
		name, field, policy string
		valid               bool
	}{
		{"static resource", "resource_scopes", `{"greeting://welcome":[]}`, true},
		{"resource template", "resource_scopes", `{"customer://records/{recordId}":["records:read"]}`, false},
		{"open resource template", "resource_scopes", `{"customer://records/{recordId}":[]}`, true},
		{"relative resource", "resource_scopes", `{"records/{recordId}":[]}`, false},
		{"malformed resource template", "resource_scopes", `{"customer://records/{recordId":[ ]}`, false},
		{"null resource policy", "resource_scopes", `null`, false},
		{"public prompt", "prompt_scopes", `{"summarize":[]}`, true},
		{"null prompt policy", "prompt_scopes", `null`, false},
		{"public prompt cannot require OAuth scope", "prompt_scopes", `{"summarize":["reports:read"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(c)
			body = []byte(strings.Replace(string(body), `"mode":"open"`, `"mode":"open","`+tc.field+`":`+tc.policy, 1))
			decoded, err := Decode(strings.NewReader(string(body)))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil {
				return
			}
			encoded, err := json.Marshal(decoded)
			if err != nil || !strings.Contains(string(encoded), `"`+tc.field+`":`+tc.policy) {
				t.Fatalf("policy lost on serialization: %s err=%v", encoded, err)
			}
		})
	}

	c.Auth = AuthConfig{
		Mode: "external-oauth", Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks",
		Resource: "https://app.example/mcp", Scopes: []string{"mcp:tools"},
		ResourceScopes: ScopePolicy{"customer://records/{recordId}": {"records:read"}},
		PromptScopes:   ScopePolicy{"summarize": {"reports:read"}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid external resource/prompt scopes: %v", err)
	}
}

func TestConfigOptionalDurableTasks(t *testing.T) {
	base := `{"version":1,"endpoint":"/mcp","transport":"streamable-http","mode":"stateless","legacy":false,"allowed_origins":[],"auth":{"mode":"open"}`
	validTasks := `,"tasks":{"enabled":true,"database_url_env":"DATABASE_URL","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID","ttl_seconds":86400,"poll_interval_ms":2000,"worker_concurrency":1}}`
	config, err := Decode(strings.NewReader(base + validTasks))
	if err != nil {
		t.Fatalf("Decode valid task config: %v", err)
	}
	if config.Tasks == nil || config.Tasks.Enabled == nil || !*config.Tasks.Enabled {
		t.Fatalf("tasks config was not retained: %+v", config.Tasks)
	}
	encoded, err := json.Marshal(config)
	if err != nil || !strings.Contains(string(encoded), `"enabled":true`) {
		t.Fatalf("task setting lost when serialized: %s err=%v", encoded, err)
	}
	if _, err := Decode(strings.NewReader(base + `,"tasks":{"enabled":false}}`)); err != nil {
		t.Fatalf("Decode disabled task config: %v", err)
	}
	for name, body := range map[string]string{
		"null task config":      base + `,"tasks":null}`,
		"missing enabled":       base + `,"tasks":{"database_url_env":"DATABASE_URL"}}`,
		"missing database name": base + `,"tasks":{"enabled":true,"owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID"}}`,
		"invalid env name":      base + `,"tasks":{"enabled":true,"database_url_env":"database_url","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID"}}`,
		"reused env name":       base + `,"tasks":{"enabled":true,"database_url_env":"SAME","owner_key_env":"SAME","namespace_env":"FAAS_APP_ID"}}`,
		"short task ttl":        base + `,"tasks":{"enabled":true,"database_url_env":"DATABASE_URL","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID","ttl_seconds":59}}`,
		"long poll interval":    base + `,"tasks":{"enabled":true,"database_url_env":"DATABASE_URL","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID","poll_interval_ms":30001}}`,
		"excessive concurrency": base + `,"tasks":{"enabled":true,"database_url_env":"DATABASE_URL","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID","worker_concurrency":17}}`,
		"negative admission":    base + `,"tasks":{"enabled":true,"database_url_env":"DATABASE_URL","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID","max_outstanding":-1}}`,
		"owner exceeds queue":   base + `,"tasks":{"enabled":true,"database_url_env":"DATABASE_URL","owner_key_env":"MCP_TASK_OWNER_KEY","namespace_env":"FAAS_APP_ID","max_outstanding":10,"max_outstanding_per_owner":11}}`,

		"unknown task field": base + `,"tasks":{"enabled":false,"mystery":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(body)); err == nil {
				t.Fatal("accepted invalid task config")
			}
		})
	}
}

func TestTaskRunningLimits(t *testing.T) {
	for _, tc := range []struct {
		total, owner int
		valid        bool
	}{
		{16, 4, true}, {1, 1, true}, {-1, 1, false}, {16, -1, false}, {1, 2, false}, {1, 0, false},
	} {
		enabled := true
		config := TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "OWNER_KEY", NamespaceEnv: "NAMESPACE", MaxRunning: tc.total, MaxRunningPerOwner: tc.owner}
		if err := config.validate(); (err == nil) != tc.valid {
			t.Errorf("running limits %d/%d: %v", tc.total, tc.owner, err)
		}
	}
}

func TestTaskRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		attempts, base, maximum int
		valid                   bool
	}{
		{0, 0, 0, true}, {1, 100, 100, true}, {10, 1000, 60000, true}, {-1, 0, 0, false}, {11, 0, 0, false}, {3, 99, 1000, false}, {3, 1000, 500, false}, {3, 100, 86400001, false},
	} {
		enabled := true
		config := TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "OWNER_KEY", NamespaceEnv: "NAMESPACE", MaxAttempts: tc.attempts, RetryBaseDelayMS: tc.base, RetryMaxDelayMS: tc.maximum}
		if err := config.validate(); (err == nil) != tc.valid {
			t.Errorf("retry policy %+v: %v", tc, err)
		}
	}
}

func TestTaskEncryptionKeyEnvironment(t *testing.T) {
	for _, name := range []string{"TASK_PAYLOAD_KEYS", "lowercase", "DATABASE_URL", "OWNER_KEY", "NAMESPACE"} {
		enabled := true
		config := TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "OWNER_KEY", NamespaceEnv: "NAMESPACE", EncryptionKeysEnv: name}
		err := config.validate()
		if (err == nil) != (name == "TASK_PAYLOAD_KEYS") {
			t.Errorf("encryption environment %q: %v", name, err)
		}
	}
}
