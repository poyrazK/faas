package api

import "testing"

func TestCodingAgentFromUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want string
	}{
		{"cli with agent", "gregale/0.1.0 (linux/amd64; agent=claude-code)", "claude-code"},
		{"agent first in comment", "gregale/0.1.0 (agent=codex; darwin/arm64)", "codex"},
		{"cli without agent", "gregale/0.1.0 (linux/amd64)", ""},
		{"unknown agent", "gregale/0.1.0 (linux/amd64; agent=my-bot)", ""},
		{"agent name with suffix", "gregale/0.1.0 (linux/amd64; agent=cursor-x)", ""},
		{"not the cli", "Mozilla/5.0 (agent=claude-code)", ""},
		{"no comment", "gregale/0.1.0", ""},
		{"unclosed comment", "gregale/0.1.0 (linux/amd64; agent=cursor", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodingAgentFromUserAgent(tt.ua); got != tt.want {
				t.Fatalf("CodingAgentFromUserAgent(%q) = %q, want %q", tt.ua, got, tt.want)
			}
		})
	}
}

func TestCodingAgentFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"none", map[string]string{}, ""},
		{"claude code", map[string]string{"CLAUDECODE": "1"}, "claude-code"},
		{"codex sandbox", map[string]string{"CODEX_SANDBOX": "seatbelt"}, "codex"},
		{"empty marker is unset", map[string]string{"CURSOR_AGENT": ""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CodingAgentFromEnv(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Fatalf("CodingAgentFromEnv(%v) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

// Every name the CLI can report must read back, or apid would drop it.
func TestEveryCodingAgentNameRoundTrips(t *testing.T) {
	for _, a := range CodingAgents {
		ua := "gregale/0.1.0 (linux/amd64; agent=" + a.Name + ")"
		if got := CodingAgentFromUserAgent(ua); got != a.Name {
			t.Fatalf("%s: CodingAgentFromUserAgent(%q) = %q", a.Marker, ua, got)
		}
	}
}
