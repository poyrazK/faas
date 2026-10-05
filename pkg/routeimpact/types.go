// Package routeimpact maps bounded FastAPI, Go HTTP, and Node HTTP source snapshots to
// HTTP routes and explains which routes may be affected by changes between Git revisions.
// Analysis never imports or executes application code.
package routeimpact

type Location struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

type Issue struct {
	Code     string `json:"code"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	Revision string `json:"revision,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
}

type Route struct {
	Method            string   `json:"method"`
	Path              string   `json:"path"`
	Handler           string   `json:"handler"`
	Source            Location `json:"source"`
	Registration      Location `json:"registration"`
	RegistrationHash  string   `json:"registration_hash,omitempty"`
	ContextFiles      []string `json:"context_files"`
	DependencyFiles   []string `json:"dependency_files"`
	HandlerSymbol     string   `json:"handler_symbol,omitempty"`
	DependencySymbols []string `json:"dependency_symbols"`
	FallbackFiles     []string `json:"fallback_files"`
}

type SymbolLocation struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

type SymbolChange struct {
	Name   string          `json:"name"`
	Change string          `json:"change"`
	Before *SymbolLocation `json:"before,omitempty"`
	After  *SymbolLocation `json:"after,omitempty"`
}

type Snapshot struct {
	Revision        string `json:"revision"`
	SourceSHA256    string `json:"source_sha256"`
	PythonFiles     int    `json:"python_files"`
	GoFiles         int    `json:"go_files,omitempty"`
	JavaScriptFiles int    `json:"javascript_files,omitempty"`
	Entrypoint      string `json:"entrypoint,omitempty"`
}

type FileChange struct {
	File   string `json:"file"`
	Change string `json:"change"`
}

// Evidence links a handler or registration context to a changed function or
// module. ViaSymbols contains static references; Via contains module imports.
// Neither establishes that the chain executes at runtime.
type Evidence struct {
	File       string           `json:"file"`
	Change     string           `json:"change"`
	Revision   string           `json:"revision"`
	Via        []string         `json:"via"`
	Kind       string           `json:"kind"`
	Symbol     string           `json:"symbol,omitempty"`
	Line       int              `json:"line,omitempty"`
	ViaSymbols []SymbolLocation `json:"via_symbols,omitempty"`
}

type Result struct {
	Method        string     `json:"method"`
	Path          string     `json:"path"`
	Change        string     `json:"change"`
	Before        *Route     `json:"before,omitempty"`
	After         *Route     `json:"after,omitempty"`
	Evidence      []Evidence `json:"evidence"`
	Precision     string     `json:"precision"`
	Uncertainties []Issue    `json:"uncertainties"`
}

type Summary struct {
	Added               int `json:"added"`
	Removed             int `json:"removed"`
	SourceChanged       int `json:"source_changed"`
	PotentiallyAffected int `json:"potentially_affected"`
	NoLinkedChanges     int `json:"no_linked_changes"`
	Unknown             int `json:"unknown"`
}

type Report struct {
	Version        int            `json:"version"`
	Framework      string         `json:"framework"`
	App            string         `json:"app,omitempty"`
	SourceRoot     string         `json:"source_root"`
	Repository     string         `json:"repository,omitempty"`
	Status         string         `json:"status"`
	Scope          string         `json:"scope"`
	Base           Snapshot       `json:"base"`
	Candidate      Snapshot       `json:"candidate"`
	ChangedFiles   []FileChange   `json:"changed_files"`
	ChangedSymbols []SymbolChange `json:"changed_symbols"`
	Summary        Summary        `json:"summary"`
	Routes         []Result       `json:"routes"`
	Issues         []Issue        `json:"issues"`
}

type Options struct {
	Path       string
	Base       string
	Head       string // Empty means the working tree, including unignored untracked files.
	Framework  string // auto, fastapi, go-nethttp (ServeMux, Chi, and Gin), or node-http (Express and Hono).
	Entrypoint string // Optional FastAPI module:variable; otherwise exactly one FastAPI instance.
	App        string // Display label only; no platform lookup or identity claim.
}

type sourceFile struct {
	path string
	hash string
	mode string
	body []byte
}

type sourceSnapshot struct {
	meta   Snapshot
	files  map[string]sourceFile
	issues []Issue
}

type sourceIndex struct {
	Entrypoint   string                     `json:"entrypoint"`
	Routes       []Route                    `json:"routes"`
	Dependencies map[string][]string        `json:"dependencies"`
	Issues       []Issue                    `json:"issues"`
	Modules      map[string]moduleSemantics `json:"modules"`
	Symbols      map[string]functionSymbol  `json:"symbols"`
}

type moduleSemantics struct {
	Hash                  string   `json:"hash"`
	InitializationHash    string   `json:"initialization_hash"`
	InitializationSymbols []string `json:"initialization_symbols"`
	Issues                []Issue  `json:"issues"`
}

type functionSymbol struct {
	Name       string   `json:"name"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Hash       string   `json:"hash"`
	References []string `json:"references"`
	Issues     []Issue  `json:"issues"`
}
