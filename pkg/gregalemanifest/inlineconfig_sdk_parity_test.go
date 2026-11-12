package gregalemanifest_test

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// inlineConfigKeys are the keys a handler may declare: every functions:
// entry field except name, root and handler, plus cron and queue.
func inlineConfigKeys() []string {
	var keys []string
	t := reflect.TypeOf(gregalemanifest.FunctionSpec{})
	for i := 0; i < t.NumField(); i++ {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if key != "" && key != "-" && key != "name" && key != "root" && key != "handler" {
			keys = append(keys, key)
		}
	}
	keys = append(keys, "cron", "queue")
	sort.Strings(keys)
	return keys
}

// adr: 20261107193000
// The SDK helpers list the same keys the CLI accepts, so a new
// functions: field cannot be missing from the typed helpers.
func TestInlineConfigKeysMatchSDKHelpers(t *testing.T) {
	want := inlineConfigKeys()

	py, err := os.ReadFile("../../sdk/python/faas_sdk/functions.py")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)FUNCTION_CONFIG_KEYS = frozenset\(\s*\{(.*?)\}`).FindSubmatch(py)
	if block == nil {
		t.Fatal("FUNCTION_CONFIG_KEYS not found in faas_sdk/functions.py")
	}
	var pyKeys []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllSubmatch(block[1], -1) {
		pyKeys = append(pyKeys, string(m[1]))
	}
	sort.Strings(pyKeys)
	if !slices.Equal(pyKeys, want) {
		t.Errorf("faas_sdk FUNCTION_CONFIG_KEYS = %v\nwant %v", pyKeys, want)
	}

	ts, err := os.ReadFile("../../sdk/node/src/function-config.ts")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)export interface FunctionConfig \{(.*?)\n\}`).FindSubmatch(ts)
	if body == nil {
		t.Fatal("interface FunctionConfig not found in function-config.ts")
	}
	var tsKeys []string
	for _, m := range regexp.MustCompile(`(?m)^\s+([a-zA-Z]+)\?:`).FindAllSubmatch(body[1], -1) {
		tsKeys = append(tsKeys, camelToSnake(string(m[1])))
	}
	sort.Strings(tsKeys)
	if !slices.Equal(tsKeys, want) {
		t.Errorf("FunctionConfig fields (as snake_case) = %v\nwant %v", tsKeys, want)
	}
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
