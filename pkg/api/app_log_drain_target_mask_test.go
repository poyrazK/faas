package api

import "testing"

func TestMaskAppLogDrainTargetURL(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"sumo logic token in the path", "https://endpoint4.collection.sumologic.com/receiver/v1/http/ZaVnC4dhaV2", "https://endpoint4.collection.sumologic.com/***"},
		{"loggly token in the path", "https://logs-01.loggly.com/inputs/TOKEN/tag/http/", "https://logs-01.loggly.com/***"},
		{"token in the query", "https://logs.example.com?api_key=secret", "https://logs.example.com/***"},
		{"empty query", "https://logs.example.com/?", "https://logs.example.com/***"},
		{"fragment", "https://logs.example.com/#secret", "https://logs.example.com/***"},
		{"port kept", "http://logs.example.com:8080/ingest", "http://logs.example.com:8080/***"},
		{"nothing past the host", "https://logs.example.com", "https://logs.example.com"},
		{"root path", "https://logs.example.com/", "https://logs.example.com/"},
		{"userinfo dropped", "https://user:pass@logs.example.com/x", "https://logs.example.com/***"},
		{"unparsable", "://", "https://***"},
	} {
		if got := MaskAppLogDrainTargetURL(tc.in); got != tc.want {
			t.Errorf("%s: MaskAppLogDrainTargetURL(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
