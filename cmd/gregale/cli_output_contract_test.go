package main_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestCLIOutputContractSelectorsAndVisibility(t *testing.T) {
	for _, kind := range []struct {
		name     string
		contract *main.CLIOutputContract
	}{
		{"build", main.NewCLIOutputContract(main.CLIOutputBuildList, []string{"all", "stream", "output"})},
		{"invocation", main.NewCLIOutputContract(main.CLIOutputInvocationList, []string{"all", "stream", "output"})},
	} {
		t.Run(kind.name, func(t *testing.T) {
			contract := kind.contract
			if contract.Scope != "json" || contract.SuccessExit != 0 || len(contract.Modes) != 4 {
				t.Fatalf("contract=%+v", contract)
			}
			for _, all := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					for _, file := range []bool{false, true} {
						state := map[string]bool{"all": all, "stream": stream, "output": file}
						var names []string
						for _, mode := range contract.Modes {
							matches := true
							for flag, want := range mode.When {
								matches = matches && state[flag] == want
							}
							if matches {
								names = append(names, mode.Name)
							}
						}
						valid := (!stream || all) && (!file || all)
						if !valid {
							if len(names) != 0 {
								t.Fatalf("invalid flags matched %q: %v", names, state)
							}
							continue
						}
						want := "page"
						if all {
							want = "all"
						}
						if stream {
							want = "stream"
						}
						if file {
							want = "file"
						}
						if len(names) != 1 || names[0] != want {
							t.Fatalf("ambiguous/wrong selection %v => %q want %s", state, names, want)
						}
					}
				}
			}
			page, all, stream, file := contract.Modes[0], contract.Modes[1], contract.Modes[2], contract.Modes[3]
			if page.Stdout.Shape != "object" || page.Stdout.CursorField != "next_cursor" || page.Stdout.Empty != "items_empty_array" {
				t.Fatalf("page=%+v", page)
			}
			if kind.name == "build" {
				if page.Stdout.ItemsField != "items" || all.Stdout.Shape != "array" || all.Stdout.ItemsField != "" || all.Stdout.Empty != "empty_array" {
					t.Fatalf("build outputs: %+v %+v", page, all)
				}
			} else if page.Stdout.ItemsField != "invocations" || all.Stdout.ItemsField != "invocations" || all.Stdout.Shape != "object" {
				t.Fatalf("invocation outputs: %+v %+v", page, all)
			}
			if stream.Stdout.Encoding != "jsonl" || stream.Stdout.Empty != "zero_bytes" || stream.Errors.ReadErrorStdout != "may_be_partial" || stream.Errors.ProgressField != "pagination" || !stream.Errors.ProgressOptional || stream.Errors.CancellationExit != 130 {
				t.Fatalf("stream=%+v", stream)
			}
			if file.Stdout.Role != "export_receipt" || file.File == nil || file.File.Overwrite || file.File.ReceiptError != "retained" || file.File.BeforePublicationStdout != "empty" || file.Errors.ProgressField != "" || file.Errors.CancellationExit != 130 {
				t.Fatalf("file=%+v", file)
			}
			data, err := json.Marshal(contract)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{`"success_exit":0`, `"all":false`, `"overwrite":false`, `"required":false`} {
				if !strings.Contains(string(data), required) {
					t.Fatalf("meaningful zero/false omitted: %s", data)
				}
			}
		})
	}
	for _, flags := range [][]string{nil, {"all"}, {"all", "stream"}, {"all", "output"}} {
		contract := main.NewCLIOutputContract(main.CLIOutputBuildList, flags)
		for _, mode := range contract.Modes {
			for flag := range mode.When {
				if !slices.Contains(flags, flag) {
					t.Fatalf("hidden flag in selector: %q %v", flag, flags)
				}
			}
			if mode.Name == "file" && !slices.Contains(flags, "output") {
				t.Fatal("hidden export mode advertised")
			}
			if mode.Name == "stream" && !slices.Contains(flags, "stream") {
				t.Fatal("hidden stream mode advertised")
			}
		}
	}
	if main.NewCLIOutputContract(0, []string{"all"}) != nil {
		t.Fatal("uncovered kind acquired a contract")
	}
}

func TestCLIOutputContractReceiptAndExitSchema(t *testing.T) {
	contract := main.NewCLIOutputContract(main.CLIOutputBuildList, []string{"all", "stream", "output"})
	fields := contract.Modes[3].Stdout.Fields
	typ := reflect.TypeFor[main.ListOutputReceipt]()
	if len(fields) != typ.NumField() {
		t.Fatalf("receipt field count: %d != %d", len(fields), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		parts := strings.Split(field.Tag.Get("json"), ",")
		found := false
		for _, description := range fields {
			if description.Name != parts[0] {
				continue
			}
			found = true
			want := "integer"
			switch field.Type.Kind() {
			case reflect.String:
				want = "string"
			case reflect.Bool:
				want = "boolean"
			}
			if description.Type != want || description.Required == slices.Contains(parts, "omitempty") {
				t.Fatalf("field=%+v descriptor=%+v", field, description)
			}
		}
		if !found {
			t.Fatalf("receipt field missing: %s", field.Name)
		}
	}
	for _, group := range contract.Errors.StatusExits {
		for _, status := range group.Statuses {
			if group.ExitCode != main.OutputContractStatusExit(status) {
				t.Fatalf("status %d exit drift", status)
			}
		}
	}
	if contract.Errors.Stream != "stderr" || contract.Errors.Shape != "problem" || contract.Errors.ExitCodeField != "exit_code" || contract.Errors.CodeField != "code" || contract.Errors.RequestIDField != "request_id" || !contract.Errors.RequestIDOptional || !contract.Errors.NativeSignalExits {
		t.Fatalf("error contract=%+v", contract.Errors)
	}
	for _, status := range []int{500, 502, 503, 599} {
		if main.OutputContractStatusExit(status) != contract.Errors.ServerErrorExit {
			t.Fatalf("server exit %d drift", status)
		}
	}
}

func TestCLIOutputContractOwnsItsMetadata(t *testing.T) {
	flags := []string{"all", "stream", "output", "fp_test_private"}
	first := main.NewCLIOutputContract(main.CLIOutputBuildList, flags)
	flags[0] = "private-input"
	first.Modes[0].When["all"] = true
	first.Modes[2].Errors.ProgressFields[0] = "private-progress"
	first.Modes[3].Stdout.Fields[0].Name = "private-field"
	first.Errors.StatusExits[0].Statuses[0] = 999
	second := main.NewCLIOutputContract(main.CLIOutputBuildList, []string{"all", "stream", "output"})
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if second.Modes[0].When["all"] || strings.Contains(string(data), "private") || strings.Contains(string(data), "999") {
		t.Fatalf("metadata retained input or mutable shared state: %s", data)
	}
}
