package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScenarioDataPreservesJSONTypesAndCSVStrings(t *testing.T) {
	jsonPath := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(jsonPath, []byte(`[{"customer":"a/b","enabled":true,"count":9007199254740993},{"customer":"second","enabled":false,"count":2}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases, fields, err := readTestData(jsonPath)
	if err != nil || len(cases) != 2 || !reflect.DeepEqual(fields, []string{"count", "customer", "enabled"}) {
		t.Fatalf("JSON cases = (%+v, %v, %v)", cases, fields, err)
	}
	if cases[0].Name != "row-1" || cases[1].Name != "row-2" || cases[0].Values["count"] != json.Number("9007199254740993") || cases[0].Values["enabled"] != true {
		t.Fatalf("JSON scalar types were lost: %+v", cases)
	}
	csvPath := filepath.Join(t.TempDir(), "cases.csv")
	if err := os.WriteFile(csvPath, []byte("customer,count,enabled\n\"first, customer\",2,true\n\"second\ncustomer\",3,false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases, fields, err = readTestData(csvPath)
	if err != nil || len(cases) != 2 || len(fields) != 3 || cases[0].Values["customer"] != "first, customer" || cases[1].Values["customer"] != "second\ncustomer" || cases[0].Values["count"] != "2" || cases[0].Values["enabled"] != "true" {
		t.Fatalf("CSV cases = (%+v, %v, %v)", cases, fields, err)
	}
	defaults, fields, err := readTestData("")
	if err != nil || len(defaults) != 1 || defaults[0].Name != "" || len(fields) != 0 {
		t.Fatalf("default case = (%+v, %v, %v)", defaults, fields, err)
	}
}

func TestScenarioDataRejectsMalformedAndUnboundedInputs(t *testing.T) {
	inputs := []struct {
		name, extension, body string
	}{
		{"empty JSON", "json", `[]`},
		{"null rows", "json", `null`},
		{"not array", "json", `{"customer":"one"}`},
		{"empty fields", "json", `[{}]`},
		{"bad field", "json", `[{"customer-name":"one"}]`},
		{"different fields", "json", `[{"a":"one"},{"b":"two"}]`},
		{"missing field", "json", `[{"a":"one"},{"a":"two","b":3}]`},
		{"nested object", "json", `[{"a":{"secret":"never-report"}}]`},
		{"nested array", "json", `[{"a":["one"]}]`},
		{"null value", "json", `[{"a":null}]`},
		{"multiple documents", "json", `[{"a":1}] [{"a":2}]`},
		{"CSV duplicate header", "csv", "a,a\none,two\n"},
		{"CSV missing value", "csv", "a,b\none\n"},
		{"CSV bad header", "csv", "Customer\none\n"},
		{"CSV no cases", "csv", "a,b\n"},
		{"unknown format", "txt", `[{"a":1}]`},
		{"too many rows", "json", "[" + strings.Repeat(`{"a":1},`, testDataCaseLimit) + `{"a":1}]`},
		{"too many CSV rows", "csv", "a\n" + strings.Repeat("value\n", testDataCaseLimit+1)},
		{"too large", "json", `[{"a":"` + strings.Repeat("x", testHTTPBodyLimit) + `"}]`},
	}
	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cases."+input.extension)
			if err := os.WriteFile(path, []byte(input.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readTestData(path); err == nil {
				t.Fatal("invalid case data was accepted")
			} else if strings.Contains(err.Error(), "never-report") {
				t.Fatalf("case data leaked into error: %v", err)
			}
		})
	}
}
