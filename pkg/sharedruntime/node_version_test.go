package sharedruntime

import "testing"

func TestNodeRuntimeForSpec(t *testing.T) {
	for _, tt := range []struct{ spec, want string }{
		{"", RuntimeNode24},
		{"22", RuntimeNode22},
		{"22.x", RuntimeNode22},
		{"22.11.0", RuntimeNode22},
		{"v22.11.0", RuntimeNode22},
		{"^22.11", RuntimeNode22},
		{"~22", RuntimeNode22},
		{"=22", RuntimeNode22},
		{"lts/jod", RuntimeNode22},
		{"24", RuntimeNode24},
		{"v24.1.0", RuntimeNode24},
		{"lts/krypton", RuntimeNode24},
		{"lts/*", RuntimeNode24},
		{">=20", RuntimeNode24},
		{">= 22.0.0", RuntimeNode24},
		{">=24", RuntimeNode24},
		{">22", RuntimeNode24},
		// A declared major neither base provides keeps its own toolchain.
		{"20", ""},
		{"^20.11", ""},
		{"18.x", ""},
		{"26", ""},
		{">=25", ""},
		{">24", ""},
		{"lts/iron", ""},
		{"node", ""},
		{"latest", ""},
		{">=20 <23", ""},
		{"22 || 24", ""},
		{"<23", ""},
	} {
		if got := NodeRuntimeForSpec(tt.spec); got != tt.want {
			t.Errorf("NodeRuntimeForSpec(%q) = %q, want %q", tt.spec, got, tt.want)
		}
	}
}
