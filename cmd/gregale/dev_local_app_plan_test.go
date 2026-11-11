package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestDevLocalAppDetection(t *testing.T) {
	for _, tc := range []struct {
		name, override, want string
		files                map[string]string
	}{
		{name: "express dev script", files: map[string]string{"package.json": `{"dependencies":{"express":"^5"},"scripts":{"dev":"node server.js"}}`}, want: "npm run dev"},
		{name: "node start fallback", files: map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`}, want: "npm run start"},
		{name: "fastapi", files: map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"}, want: "python3 -m uvicorn main:app --host 127.0.0.1 --port $PORT"},
		{name: "go", files: map[string]string{"go.mod": "module example.com/app\n"}, want: "go run ."},
		{name: "manifest command", files: map[string]string{"gregale.yaml": "dev:\n  watch:\n    command: node custom.js\n"}, want: "node custom.js"},
		{name: "explicit wins", files: map[string]string{"gregale.yaml": "dev:\n  watch:\n    command: node old.js\n"}, override: "node custom.js", want: "node custom.js"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeDevLocalProject(t, tc.files)
			command, framework, function, err := main.PlanDevLocalWorkloadForTest(dir, tc.override)
			if err != nil || function || command != tc.want || framework == "" {
				t.Fatalf("plan = %q %q function=%v err=%v; want %q", command, framework, function, err, tc.want)
			}
		})
	}
}

func TestDevLocalAppKeepsFunctionPathAndRejectsInvalidConfig(t *testing.T) {
	dir := writeDevLocalProject(t, map[string]string{"handler.py": "def handler(event, ctx): return {'body': 'ok'}\n"})
	_, _, function, err := main.PlanDevLocalWorkloadForTest(dir, "")
	if err != nil || !function {
		t.Fatalf("function was routed to app: function=%v err=%v", function, err)
	}
	for _, files := range []map[string]string{
		{},
		{"gregale.yaml": "functions: [invalid"},
		{"gregale.yaml": "function:\n  runtime: invalid\n", "package.json": `{"scripts":{"dev":"node server.js"}}`},
	} {
		if _, _, _, err := main.PlanDevLocalWorkloadForTest(writeDevLocalProject(t, files), ""); err == nil {
			t.Fatalf("invalid or unsupported project accepted: %v", files)
		}
	}
	if _, _, _, err := main.PlanDevLocalWorkloadForTest(dir, "node custom.js"); err == nil || !strings.Contains(err.Error(), "functions") {
		t.Fatalf("function accepted app command: %v", err)
	}
}
