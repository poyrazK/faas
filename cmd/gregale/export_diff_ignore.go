package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

type exportDiffIgnorePathFlags []string

func (paths *exportDiffIgnorePathFlags) String() string { return strings.Join(*paths, ",") }
func (paths *exportDiffIgnorePathFlags) Set(value string) error {
	*paths = append(*paths, value)
	return nil
}

type exportDiffIgnoreNode struct {
	terminal bool
	children map[string]*exportDiffIgnoreNode
}

type exportDiffReadOptions struct {
	explain bool
	paths   []string
	ignores *exportDiffIgnoreNode
}

func newExportDiffReadOptions(key string, explain bool, paths []string) (exportDiffReadOptions, error) {
	options := exportDiffReadOptions{explain: explain}
	if len(paths) == 0 {
		return options, nil
	}
	options.ignores = &exportDiffIgnoreNode{}
	unique := make(map[string]bool, len(paths))
	for _, path := range paths {
		tokens, err := exportDiffIgnoreTokens(path)
		if err != nil {
			return exportDiffReadOptions{}, err
		}
		if tokens[0] == key {
			return exportDiffReadOptions{}, errors.New("ignored paths must not select the record key or its descendants")
		}
		node := options.ignores
		for _, token := range tokens {
			if node.children == nil {
				node.children = make(map[string]*exportDiffIgnoreNode)
			}
			if node.children[token] == nil {
				node.children[token] = &exportDiffIgnoreNode{}
			}
			node = node.children[token]
		}
		node.terminal = true
		if !unique[path] {
			unique[path] = true
			options.paths = append(options.paths, path)
		}
	}
	sort.Strings(options.paths)
	return options, nil
}

func exportDiffIgnoreTokens(path string) ([]string, error) {
	if !utf8.ValidString(path) || !strings.HasPrefix(path, "/") {
		return nil, errors.New("ignored paths must be non-root UTF-8 JSON Pointers starting with '/'")
	}
	tokens := strings.Split(path[1:], "/")
	for index, token := range tokens {
		for i := 0; i < len(token); i++ {
			if token[i] != '~' {
				continue
			}
			if i+1 == len(token) || token[i+1] != '0' && token[i+1] != '1' {
				return nil, errors.New("ignored paths must use only '~0' and '~1' JSON Pointer escapes")
			}
			i++
		}
		tokens[index] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return tokens, nil
}

// Parsing and duplicate checks finish before filtering. Only object members
// are projected out; surviving empty containers and array order are preserved.
func filterExportDiffValue(value exportDiffValue, ignores *exportDiffIgnoreNode) (exportDiffValue, error) {
	if ignores == nil || value.kind != 'o' {
		return value, nil
	}
	changed := false
	for name, selection := range ignores.children {
		child, found := value.members[name]
		if !found {
			continue
		}
		if selection.terminal {
			delete(value.members, name)
			changed = true
			continue
		}
		if child.kind == 'a' {
			return exportDiffValue{}, errors.New("ignored paths cannot select array elements; select the whole array field")
		}
		filtered, err := filterExportDiffValue(child, selection)
		if err != nil {
			return exportDiffValue{}, err
		}
		if filtered.digest != child.digest {
			changed = true
		}
		value.members[name] = filtered
	}
	if changed {
		return exportDiffObjectValue(value.members), nil
	}
	return value, nil
}

func appendExportDiffIgnoredPaths(output *bytes.Buffer, paths []string) {
	if len(paths) == 0 {
		return
	}
	encoded, _ := json.Marshal(paths)
	_, _ = fmt.Fprintf(output, "Ignored paths: %s\n", encoded)
}
