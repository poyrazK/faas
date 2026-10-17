package rehearsalcommand

import (
	"strings"
	"testing"
)

func testSpec() Spec {
	return Spec{Version: 1, RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"node", "import.js"}, Env: map[string]string{"LABEL": strings.Repeat("界", 600)}, Files: map[string][]byte{"fixtures/input.json": []byte(strings.Repeat("x", 6000))}}
}

func TestCommandEnvelopePinsUTF8InputsAndRejectsForeignReceipts(t *testing.T) {
	s := testSpec()
	argv, err := Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 3 {
		t.Fatal("test did not cross argument boundary")
	}
	decoded, err := Decode(argv)
	if err != nil || decoded.SHA256() != s.SHA256() {
		t.Fatalf("envelope changed: %v", err)
	}
	id := "22222222-2222-2222-2222-222222222222"
	raw, err := (Receipt{Version: 1, TaskID: id, SpecSHA256: s.SHA256(), Behavior: []byte(`{"version":1}`)}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseReceipt(raw, id, s); err != nil {
		t.Fatal(err)
	}
	s.Files["fixtures/input.json"] = []byte("changed")
	if _, err := ParseReceipt(raw, id, s); err == nil {
		t.Fatal("receipt accepted different inputs")
	}
	if _, err := ParseReceipt(raw, "33333333-3333-3333-3333-333333333333", decoded); err == nil {
		t.Fatal("foreign execution receipt accepted")
	}
}

func TestCommandEnvelopeRejectsUnsupportedInputs(t *testing.T) {
	for _, mode := range []string{"parent", "absolute", "overflow", "reserved-env", "unknown", "trailing", "nested"} {
		t.Run(mode, func(t *testing.T) {
			s := testSpec()
			switch mode {
			case "parent":
				s.Files = map[string][]byte{"../outside": nil}
			case "absolute":
				s.Files = map[string][]byte{"/outside": nil}
			case "overflow":
				s.Files = map[string][]byte{"input": make([]byte, MaxInputBytes+1)}
			case "reserved-env":
				s.Env = map[string]string{"HTTP_PROXY": "https://live.invalid"}
			case "nested":
				s.Command = []string{Executable}
			}
			if mode == "unknown" || mode == "trailing" {
				argv, _ := Encode(s)
				if mode == "unknown" {
					argv[len(argv)-1] = strings.TrimSuffix(argv[len(argv)-1], "}") + `,"extra":true}`
				} else {
					argv = append(argv, "{}")
				}
				if _, err := Decode(argv); err == nil {
					t.Fatal("malformed envelope accepted")
				}
				return
			}
			if _, err := Encode(s); err == nil {
				t.Fatal("unsupported input accepted")
			}
		})
	}
}

func TestCommandReceiptDistinguishesMissingAndEmptyArtifacts(t *testing.T) {
	s := testSpec()
	id := "22222222-2222-2222-2222-222222222222"
	for _, present := range []bool{false, true} {
		r := Receipt{Version: 1, TaskID: id, SpecSHA256: s.SHA256()}
		if present {
			r.Behavior = []byte{}
		}
		raw, err := r.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := ParseReceipt(raw, id, s)
		if err != nil {
			t.Fatal(err)
		}
		if (decoded.Behavior != nil) != present {
			t.Fatal("empty output became indistinguishable from an absent output")
		}
	}
}
