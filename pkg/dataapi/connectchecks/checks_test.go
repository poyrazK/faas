package connectchecks

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const example = `{"version":1,"status":"reviewed","relation":"notes","key_column":"id","identities":["alice","bob"],"fixtures":[{"name":"alice_note","key":101,"visible_to":["alice"]},{"name":"bob_note","key":202,"visible_to":["bob"]}]}`

func TestBuildExpandsReviewedVisibilityMatrix(t *testing.T) {
	spec, err := Decode([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	collection, summary, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Fixtures: 2, Allowed: 2, Denied: 2}) || len(collection.Requests) != 4 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	for i, request := range collection.Requests {
		parsed, err := url.Parse(request.Path)
		if err != nil {
			t.Fatal(err)
		}
		wantKey := "eq.101"
		if i >= 2 {
			wantKey = "eq.202"
		}
		if request.Method != "GET" || parsed.Path != "/rest/v1/notes" || parsed.Query().Get("select") != "id" || parsed.Query().Get("id") != wantKey || parsed.Query().Get("limit") != "2" {
			t.Fatalf("unexpected request: %+v", request)
		}
		rows := request.Expect.JSON.([]any)
		if (i == 0 || i == 3) != (len(rows) == 1) || request.Expect.Status != 200 {
			t.Fatalf("unexpected expectation: %+v", request)
		}
	}
}

func TestDecodeAndBuildRejectAmbiguousOrUnsafeSpecifications(t *testing.T) {
	for _, body := range []string{
		strings.Replace(example, `"status":"reviewed"`, `"status":"draft"`, 1),
		strings.Replace(example, `"visible_to":["alice"]`, `"visible_to":[]`, 1),
		strings.Replace(example, `"visible_to":["bob"]`, `"visible_to":["alice"]`, 1),
		strings.Replace(example, `"visible_to":["bob"]`, `"visible_to":["mallory"]`, 1),
		strings.Replace(example, `"key":202`, `"key":"101"`, 1),
		strings.Replace(example, `"key":202`, `"key":9007199254740992`, 1),
		strings.Replace(example, `"key":202`, `"key":2.5`, 1),
		strings.Replace(example, `"key":202`, `"key":"a&limit=100"`, 1),
		strings.Replace(example, `"relation":"notes"`, `"relation":"rpc"`, 1),
		strings.Replace(example, `"key_column":"id"`, `"key_column":"limit"`, 1),
		strings.Replace(example, `"version":1`, `"version":1,"version":2`, 1),
		strings.Replace(example, `"version":1`, `"version":1,"unknown":true`, 1),
		example + `{}`,
	} {
		spec, err := Decode([]byte(body))
		if err == nil {
			_, _, err = Build(spec)
		}
		if err == nil {
			t.Fatalf("accepted invalid input: %s", body)
		}
	}
}

func TestSharedRowsRemainExplicit(t *testing.T) {
	spec, err := Decode([]byte(strings.ReplaceAll(strings.ReplaceAll(example, `["alice"]`, `["alice","bob"]`), `["bob"]`, `["alice","bob"]`)))
	if err != nil {
		t.Fatal(err)
	}
	_, summary, err := Build(spec)
	if err != nil || summary.Allowed != 4 || summary.Denied != 0 {
		t.Fatalf("shared matrix: %+v %v", summary, err)
	}
}

func TestGeneratedCollectionWorksWithHostedReplay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	spec, err := Decode([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	collection, _, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "checks.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("..", "..", "..", "tests", "data-api", "connect-checks-replay.mjs"), path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hosted replay integration: %v\n%s", err, output)
	}
}
