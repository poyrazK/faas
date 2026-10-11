package routeimpact

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed typescript-6.0.3.js.gz
var typescriptParserGzip []byte

//go:embed node_analyzer.js
var nodeRouteAnalyzer string

func nodeSourcePath(path string) bool {
	switch filepath.Ext(path) {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func indexNodeHTTP(ctx context.Context, snapshot sourceSnapshot) (sourceIndex, error) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return sourceIndex{}, errors.New("run isolated JavaScript and TypeScript parser (requires node)")
	}
	tempDir, err := os.MkdirTemp("", "gregale-routeimpact-node-")
	if err != nil {
		return sourceIndex{}, fmt.Errorf("prepare isolated route parser: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	parserPath := filepath.Join(tempDir, "typescript.js")
	parserFile, err := os.OpenFile(parserPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sourceIndex{}, fmt.Errorf("prepare isolated route parser: %w", err)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(typescriptParserGzip))
	if err != nil {
		_ = parserFile.Close()
		return sourceIndex{}, errors.New("load bundled TypeScript parser")
	}
	const parserMaxBytes = 16 << 20
	written, copyErr := io.Copy(parserFile, io.LimitReader(compressed, parserMaxBytes+1))
	closeParserErr, closeFileErr := compressed.Close(), parserFile.Close()
	if copyErr != nil || closeParserErr != nil || closeFileErr != nil || written > parserMaxBytes {
		return sourceIndex{}, errors.New("load bundled TypeScript parser")
	}
	type source struct {
		File string `json:"file"`
		Body []byte `json:"body"`
	}
	sources := make([]source, 0, snapshot.meta.JavaScriptFiles)
	for path, file := range snapshot.files {
		if nodeSourcePath(path) && file.body != nil {
			sources = append(sources, source{File: path, Body: file.body})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].File < sources[j].File })
	payload, err := json.Marshal(struct {
		Sources []source       `json:"sources"`
		Limits  map[string]int `json:"limits"`
	}{sources, map[string]int{
		"routes": api.RouteImpactMaxRoutes, "issues": api.RouteImpactMaxIssues,
		"edges": api.RouteImpactMaxImportEdges, "depth": api.RouteImpactMaxGraphDepth,
		"symbols": api.RouteImpactMaxSymbols, "symbol_edges": api.RouteImpactMaxSymbolEdges,
		"symbol_issues": api.RouteImpactMaxSymbolIssues,
	}})
	if err != nil {
		return sourceIndex{}, fmt.Errorf("encode static analysis input: %w", err)
	}
	parserCtx, cancel := context.WithTimeout(ctx, api.RouteImpactParserTimeout)
	defer cancel()
	cmd := exec.CommandContext(parserCtx, nodePath, "--max-old-space-size=512", "-e", nodeRouteAnalyzer, parserPath)
	cmd.Dir = tempDir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C", "TZ=UTC"}
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = io.Discard
	out := &limitedBuffer{max: api.RouteImpactASTOutputMaxBytes}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return sourceIndex{}, fmt.Errorf("run isolated JavaScript and TypeScript parser (requires node): %w", err)
	}
	var index sourceIndex
	if err := json.Unmarshal(out.Bytes(), &index); err != nil {
		return sourceIndex{}, fmt.Errorf("decode static JavaScript route index: %w", err)
	}
	index.Issues = append(index.Issues, snapshot.issues...)
	if len(index.Issues) > api.RouteImpactMaxIssues {
		return sourceIndex{}, errors.New("static route index exceeds the issue limit")
	}
	return index, nil
}
