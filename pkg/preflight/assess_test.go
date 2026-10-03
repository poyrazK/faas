package preflight

import (
	"testing"
	"testing/fstest"
)

// A hard disqualifier outranks any number of inference warnings. An app that
// cannot run must never be reported as merely needing a tweak.
func TestAssess_RedOutranksAmber(t *testing.T) {
	fsys := fstest.MapFS{
		"Dockerfile":   &fstest.MapFile{Data: []byte("FROM node:22\nVOLUME /data\n")},
		"package.json": &fstest.MapFile{Data: []byte(`{"name":"api"}`)},
	}

	verdict, err := Assess(fsys)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	if verdict.Level != LevelRed {
		t.Fatalf("Level = %q, want %q (findings %+v)", verdict.Level, LevelRed, verdict.Findings)
	}
}

// A conventional Node service with a start script and no disqualifiers is the
// case the whole tool exists to say yes to.
func TestAssess_CleanNodeServiceIsGreen(t *testing.T) {
	fsys := fstest.MapFS{
		"package.json": &fstest.MapFile{Data: []byte(`{"name":"api","scripts":{"start":"node server.js"}}`)},
		"server.js":    &fstest.MapFile{Data: []byte("require('http').createServer().listen(process.env.PORT, '0.0.0.0')\n")},
	}

	verdict, err := Assess(fsys)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}

	if verdict.Level != LevelGreen {
		t.Fatalf("Level = %q, want %q (findings %+v)", verdict.Level, LevelGreen, verdict.Findings)
	}
	if verdict.Profile.StartCommand == "" {
		t.Error("verdict carries no start command; the report needs it")
	}
}
