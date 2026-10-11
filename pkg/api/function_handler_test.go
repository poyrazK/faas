package api

import "testing"

func TestFunctionHandlerFile(t *testing.T) {
	tests := []struct {
		runtime, handler string
		want             string
		ok, err          bool
	}{
		{"node24", "handler.handler", "", false, false},     // historical value keeps the default
		{"node24", "handler.js", "handler.js", true, false}, // default file, named explicitly
		{"node22", "./api/resize.js", "api/resize.js", true, false},
		{"node24", "api/digest.mjs", "api/digest.mjs", true, false},
		{"python313", "api/digest.py", "api/digest.py", true, false},
		{"python312", "api/digest.js", "", false, false}, // wrong extension for the runtime
		{"go124", "cmd/thumbs/main.go", "", false, false},
		{"node24", "/app/api/resize.js", "", false, true},
		{"node24", "../shared/resize.js", "", false, true},
		{"node24", "api/a b.js", "", false, true},
		{"python313", "api/x;rm.py", "", false, true},
	}
	for _, tt := range tests {
		got, ok, err := FunctionHandlerFile(tt.runtime, tt.handler)
		if got != tt.want || ok != tt.ok || (err != nil) != tt.err {
			t.Errorf("FunctionHandlerFile(%q, %q) = (%q, %v, %v), want (%q, %v, err=%v)", tt.runtime, tt.handler, got, ok, err, tt.want, tt.ok, tt.err)
		}
	}
}
