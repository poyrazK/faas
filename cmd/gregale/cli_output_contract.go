package main

import "slices"

type cliOutputKind uint8

const (
	cliOutputBuildList cliOutputKind = iota + 1
	cliOutputInvocationList
)

// Conditions describe effective flags on valid JSON-mode invocations. A
// missing condition is unconstrained; output means explicit flag presence.
type cliOutputContract struct {
	Scope       string                 `json:"scope"`
	SuccessExit int                    `json:"success_exit"`
	Modes       []cliOutputMode        `json:"modes"`
	Errors      cliOutputErrorContract `json:"errors"`
}

type cliOutputMode struct {
	Name     string               `json:"name"`
	When     map[string]bool      `json:"when"`
	Stdout   cliOutputStdout      `json:"stdout"`
	Errors   cliOutputModeErrors  `json:"errors"`
	File     *cliOutputFileFormat `json:"file,omitempty"`
	Progress *cliOutputProgress   `json:"progress,omitempty"`
}

type cliOutputProgress struct {
	Flag                string   `json:"flag"`
	Stream              string   `json:"stream"`
	Default             string   `json:"default"`
	NDJSONType          string   `json:"ndjson_type"`
	SchemaVersion       int      `json:"schema_version"`
	Events              []string `json:"events"`
	MinUpdateIntervalMS int64    `json:"min_update_interval_ms"`
	BestEffort          bool     `json:"best_effort"`
}

type cliOutputStdout struct {
	Encoding    string           `json:"encoding"`
	Shape       string           `json:"shape"`
	ItemsField  string           `json:"items_field,omitempty"`
	CursorField string           `json:"cursor_field,omitempty"`
	Empty       string           `json:"empty"`
	Emission    string           `json:"emission"`
	Role        string           `json:"role,omitempty"`
	Fields      []cliOutputField `json:"fields,omitempty"`
}

type cliOutputField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type cliOutputModeErrors struct {
	ReadErrorStdout  string   `json:"read_error_stdout"`
	StdoutWriteError string   `json:"stdout_write_error"`
	ProgressField    string   `json:"progress_field,omitempty"`
	ProgressFields   []string `json:"progress_fields,omitempty"`
	ProgressOptional bool     `json:"progress_optional,omitempty"`
	CancellationExit int      `json:"cancellation_exit,omitempty"`
}

type cliOutputFileFormat struct {
	Encoding                string `json:"encoding"`
	Empty                   string `json:"empty"`
	Publication             string `json:"publication"`
	Overwrite               bool   `json:"overwrite"`
	BeforePublicationError  string `json:"before_publication_error"`
	BeforePublicationStdout string `json:"before_publication_stdout"`
	ReceiptError            string `json:"receipt_error"`
}

type cliOutputErrorContract struct {
	Stream            string              `json:"stream"`
	Encoding          string              `json:"encoding"`
	Shape             string              `json:"shape"`
	ExitCodeField     string              `json:"exit_code_field"`
	CodeField         string              `json:"code_field"`
	RequestIDField    string              `json:"request_id_field"`
	RequestIDOptional bool                `json:"request_id_optional"`
	StatusExits       []cliOutputHTTPExit `json:"status_exits"`
	ServerErrorExit   int                 `json:"server_error_exit"`
	OtherAPIErrorExit int                 `json:"other_api_error_exit"`
	LocalErrorExit    int                 `json:"local_error_exit"`
	TransportExit     int                 `json:"transport_exit"`
	NativeSignalExits bool                `json:"native_signal_exits"`
}

type cliOutputHTTPExit struct {
	Statuses []int `json:"statuses"`
	ExitCode int   `json:"exit_code"`
}

func newCLIOutputContract(kind cliOutputKind, available []string) *cliOutputContract {
	items := "items"
	switch kind {
	case cliOutputBuildList:
	case cliOutputInvocationList:
		items = "invocations"
	default:
		return nil
	}
	contract := &cliOutputContract{Scope: "json", Modes: []cliOutputMode{}, Errors: listOutputErrorContract()}
	for _, mode := range []cliOutputMode{listPageOutputMode(items), listAllOutputMode(items), listStreamOutputMode(), listFileOutputMode()} {
		visible := true
		for name, enabled := range mode.When {
			if !slices.Contains(available, name) {
				if enabled {
					visible = false
					break
				}
				delete(mode.When, name)
			}
		}
		if visible {
			if mode.File != nil && slices.Contains(available, "progress") {
				mode.Progress = &cliOutputProgress{Flag: "progress", Stream: "stderr", Default: string(listExportProgressOff), NDJSONType: "list_export_progress", SchemaVersion: 1,
					Events: []string{"start", "update", "stopped", "published"}, MinUpdateIntervalMS: listExportProgressInterval.Milliseconds(), BestEffort: true}
			}
			contract.Modes = append(contract.Modes, mode)
		}
	}
	return contract
}

func listPageOutputMode(items string) cliOutputMode {
	return cliOutputMode{Name: "page", When: map[string]bool{"all": false, "stream": false, "output": false},
		Stdout: cliOutputStdout{Encoding: "json", Shape: "object", ItemsField: items, CursorField: "next_cursor", Empty: "items_empty_array", Emission: "after_complete_read"},
		Errors: cliOutputModeErrors{ReadErrorStdout: "empty", StdoutWriteError: "may_be_partial"}}
}

func listAllOutputMode(items string) cliOutputMode {
	mode := listPageOutputMode(items)
	mode.Name, mode.When["all"], mode.Stdout.CursorField = "all", true, ""
	if items == "items" {
		mode.Stdout.Shape, mode.Stdout.ItemsField, mode.Stdout.Empty = "array", "", "empty_array"
	}
	return mode
}

func listStreamOutputMode() cliOutputMode {
	return cliOutputMode{Name: "stream", When: map[string]bool{"all": true, "stream": true, "output": false},
		Stdout: cliOutputStdout{Encoding: "jsonl", Shape: "records", Empty: "zero_bytes", Emission: "per_page"},
		Errors: cliOutputModeErrors{ReadErrorStdout: "may_be_partial", StdoutWriteError: "may_be_partial",
			ProgressField: "pagination", ProgressFields: []string{"pages_completed", "items_emitted", "partial"}, ProgressOptional: true, CancellationExit: 130}}
}

func listFileOutputMode() cliOutputMode {
	return cliOutputMode{Name: "file", When: map[string]bool{"all": true, "output": true},
		Stdout: cliOutputStdout{Encoding: "json", Shape: "object", Empty: "receipt", Emission: "after_file_publication", Role: "export_receipt", Fields: []cliOutputField{
			{Name: "path", Type: "string", Required: false}, {Name: "format", Type: "string", Required: true}, {Name: "published", Type: "boolean", Required: true},
			{Name: "pages_completed", Type: "integer", Required: true}, {Name: "items_emitted", Type: "integer", Required: true},
			{Name: "bytes_written", Type: "integer", Required: true}, {Name: "sha256", Type: "string", Required: true},
		}},
		File: &cliOutputFileFormat{Encoding: "jsonl", Empty: "zero_bytes", Publication: "atomic_no_replace", Overwrite: false,
			BeforePublicationError: "not_published", BeforePublicationStdout: "empty", ReceiptError: "retained"},
		Errors: cliOutputModeErrors{ReadErrorStdout: "empty", StdoutWriteError: "may_be_partial", CancellationExit: 130}}
}

func listOutputErrorContract() cliOutputErrorContract {
	errors := cliOutputErrorContract{Stream: "stderr", Encoding: "json", Shape: "problem", ExitCodeField: "exit_code", CodeField: "code",
		RequestIDField: "request_id", RequestIDOptional: true, ServerErrorExit: exitCodeForStatus(500), OtherAPIErrorExit: exitCodeForStatus(400),
		LocalErrorExit: 1, TransportExit: 3, NativeSignalExits: true}
	for _, statuses := range [][]int{{401}, {403}, {404, 410}, {409, 412}, {408, 429}, {501}} {
		errors.StatusExits = append(errors.StatusExits, cliOutputHTTPExit{Statuses: statuses, ExitCode: exitCodeForStatus(statuses[0])})
	}
	return errors
}
