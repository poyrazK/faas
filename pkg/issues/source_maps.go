package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const DeploymentSourceMapStrategy = "deployment-source-maps-v1"

type sourceMapSegment struct {
	column, source, line, originalColumn, name int
	mapped                                     bool
}
type compiledSourceMap struct {
	sources, names []string
	lines          [][]sourceMapSegment
}
type SourceMapSet struct {
	Request api.PublishIssueSourceMapsRequest
	Digest  string
	maps    map[string]compiledSourceMap
}
type flatSourceMap struct {
	Version    int             `json:"version"`
	SourceRoot string          `json:"sourceRoot,omitempty"`
	Sources    []string        `json:"sources"`
	Names      []string        `json:"names"`
	Mappings   string          `json:"mappings"`
	Sections   json.RawMessage `json:"sections,omitempty"`
}

var errSourceMap = errors.New("invalid or oversized flat Source Map v3 manifest")

func sourceMapFile(file string) (string, error) {
	if len(file) > api.IssueMaxFrameBytes || !utf8.ValidString(file) || strings.ContainsAny(file, "\\\x00") {
		return "", errSourceMap
	}
	if strings.HasPrefix(file, "/") && !strings.HasPrefix(file, "//") {
		return path.Clean(file), nil
	}
	u, err := url.Parse(file)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") {
		return "", errSourceMap
	}
	return path.Clean(u.Path), nil
}
func portableSourceMapFile(file string) bool {
	return file != "" && len(file) <= api.IssueMaxFrameBytes && utf8.ValidString(file) &&
		!strings.ContainsAny(file, "\\:\x00") && !strings.HasPrefix(file, "/") && file != ".." &&
		!strings.HasPrefix(file, "../") && path.Clean(file) == file
}
func originalSource(source, root, generated string) (string, error) {
	base := &url.URL{Scheme: "file", Path: generated}
	if root != "" {
		if !strings.HasSuffix(root, "/") {
			root += "/"
		}
		u, err := url.Parse(root)
		if err != nil {
			return "", errSourceMap
		}
		base = base.ResolveReference(u)
	}
	u, err := url.Parse(source)
	if err != nil {
		return "", errSourceMap
	}
	u = base.ResolveReference(u)
	if u.User != nil {
		return "", errSourceMap
	}
	result := u.String()
	if u.Scheme == "file" {
		result = u.Path
	}
	if result == "" || len(result) > api.IssueMaxFrameBytes {
		return "", errSourceMap
	}
	return result, nil
}

// CompileSourceMaps strips source contents/extensions and checks amplification
// bounds before allocating decoded rows. Source files and map URLs are never read.
func CompileSourceMaps(in api.PublishIssueSourceMapsRequest) (*SourceMapSet, error) {
	directory, err := sourceMapFile(in.Directory)
	if err != nil || in.Manifest.Version != 1 || len(in.Manifest.Artifacts) == 0 || len(in.Manifest.Artifacts) > api.IssueSourceMapsMax {
		return nil, errSourceMap
	}
	in.Directory = directory
	in.Manifest.Artifacts = append([]api.IssueSourceMapArtifact(nil), in.Manifest.Artifacts...)
	sort.Slice(in.Manifest.Artifacts, func(i, j int) bool {
		return in.Manifest.Artifacts[i].GeneratedFile < in.Manifest.Artifacts[j].GeneratedFile
	})
	set := &SourceMapSet{maps: make(map[string]compiledSourceMap)}
	bytes, symbols, entries := 0, 0, 0
	for i, artifact := range in.Manifest.Artifacts {
		if !portableSourceMapFile(artifact.GeneratedFile) || len(artifact.SHA256) != 64 || strings.ToLower(artifact.SHA256) != artifact.SHA256 {
			return nil, errSourceMap
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return nil, errSourceMap
		}
		generated, err := sourceMapFile(path.Join(directory, artifact.GeneratedFile))
		if err != nil {
			return nil, err
		}
		if _, exists := set.maps[generated]; exists {
			return nil, errSourceMap
		}
		bytes += len(artifact.Payload)
		if len(artifact.Payload) > api.IssueSourceMapMaxBytes || bytes > api.IssueSourceMapsMaxBytes || !utf8.ValidString(artifact.Payload) {
			return nil, errSourceMap
		}
		var m flatSourceMap
		if err := json.Unmarshal([]byte(artifact.Payload), &m); err != nil || m.Version != 3 || m.Sections != nil || m.Sources == nil || m.Names == nil || len(m.SourceRoot) > api.IssueMaxFrameBytes {
			return nil, errSourceMap
		}
		symbols += len(m.Sources) + len(m.Names)
		if symbols > api.IssueSourceMapSymbolsMax {
			return nil, errSourceMap
		}
		compiled := compiledSourceMap{names: append([]string(nil), m.Names...)}
		for _, name := range m.Names {
			if len(name) > api.IssueMaxFrameBytes || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
				return nil, errSourceMap
			}
		}
		for _, source := range m.Sources {
			if source == "" || len(source) > api.IssueMaxFrameBytes || !utf8.ValidString(source) {
				return nil, errSourceMap
			}
			original, err := originalSource(source, m.SourceRoot, generated)
			if err != nil {
				return nil, err
			}
			compiled.sources = append(compiled.sources, original)
		}
		compiled.lines, err = decodeSourceMappings(m.Mappings, len(m.Sources), len(m.Names), &entries)
		if err != nil {
			return nil, err
		}
		m.Sections = nil
		raw, err := json.Marshal(m)
		if err != nil || len(raw) > api.IssueSourceMapMaxBytes {
			return nil, errSourceMap
		}
		in.Manifest.Artifacts[i].Payload = string(raw)
		set.maps[generated] = compiled
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > api.IssueSourceMapsUploadMaxBytes {
		return nil, errSourceMap
	}
	sum := sha256.Sum256(raw)
	set.Digest = hex.EncodeToString(sum[:])
	set.Request = in
	return set, nil
}

func sourceVLQ(encoded string, offset *int) (int, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var value uint32
	for digits := 0; digits < 7; digits++ {
		if *offset >= len(encoded) {
			return 0, errSourceMap
		}
		digit := strings.IndexByte(alphabet, encoded[*offset])
		*offset++
		if digit < 0 || (digits == 6 && digit > 3) {
			return 0, errSourceMap
		}
		value |= uint32(digit&31) << uint(digits*5)
		if digit&32 == 0 {
			result := int(value >> 1)
			if value&1 != 0 {
				result = -result
			}
			return result, nil
		}
	}
	return 0, errSourceMap
}
func decodeSourceMappings(encoded string, sources, names int, entries *int) ([][]sourceMapSegment, error) {
	rows := strings.Count(encoded, ";") + 1
	if rows > api.IssueSourceMapEntriesMax-*entries {
		return nil, errSourceMap
	}
	*entries += rows
	lines := make([][]sourceMapSegment, 0, rows)
	source, line, originalColumn, name := 0, 0, 0, 0
	for _, row := range strings.Split(encoded, ";") {
		var segments []sourceMapSegment
		column := 0
		if row != "" {
			count := strings.Count(row, ",") + 1
			if count > api.IssueSourceMapEntriesMax-*entries {
				return nil, errSourceMap
			}
			*entries += count
			for _, encodedSegment := range strings.Split(row, ",") {
				var fields []int
				for offset := 0; offset < len(encodedSegment); {
					value, err := sourceVLQ(encodedSegment, &offset)
					if err != nil {
						return nil, err
					}
					fields = append(fields, value)
					if len(fields) > 5 {
						return nil, errSourceMap
					}
				}
				if len(fields) != 1 && len(fields) != 4 && len(fields) != 5 {
					return nil, errSourceMap
				}
				if fields[0] < 0 || int64(column)+int64(fields[0]) > api.IssueFrameCoordinateMax {
					return nil, errSourceMap
				}
				column += fields[0]
				segment := sourceMapSegment{column: column, name: -1}
				if len(fields) >= 4 {
					source += fields[1]
					line += fields[2]
					originalColumn += fields[3]
					if source < 0 || source >= sources || line < 0 || line >= api.IssueFrameCoordinateMax || originalColumn < 0 || originalColumn >= api.IssueFrameCoordinateMax {
						return nil, errSourceMap
					}
					segment.source = source
					segment.line = line
					segment.originalColumn = originalColumn
					segment.mapped = true
					if len(fields) == 5 {
						name += fields[4]
						if name < 0 || name >= names {
							return nil, errSourceMap
						}
						segment.name = name
					}
				}
				segments = append(segments, segment)
			}
		}
		lines = append(lines, segments)
	}
	return lines, nil
}

func (s *SourceMapSet) Symbolicate(event api.IssueEvent) (api.IssueEvent, api.IssueSymbolicationResult) {
	result := api.IssueSymbolicationResult{ManifestSHA256: s.Digest}
	resolve := func(frames []api.IssueFrame) []api.IssueFrame {
		frames = append([]api.IssueFrame(nil), frames...)
		for i, f := range frames {
			mapped := false
			file, err := sourceMapFile(f.File)
			m, exists := s.maps[file]
			if err == nil && exists && f.Line > 0 && f.Line <= len(m.lines) && f.Column > 0 {
				row := m.lines[f.Line-1]
				index := sort.Search(len(row), func(i int) bool { return row[i].column > f.Column-1 }) - 1
				if index >= 0 && row[index].mapped {
					entry := row[index]
					original := m.sources[entry.source]
					function := "<anonymous>"
					if entry.name >= 0 && strings.TrimSpace(m.names[entry.name]) != "" {
						function = strings.TrimSpace(m.names[entry.name])
					}
					frames[i] = api.IssueFrame{File: original, Function: function, Line: entry.line + 1, Column: entry.originalColumn + 1, InApp: !strings.HasPrefix(original, "node:") && !strings.Contains(strings.ReplaceAll(original, "\\", "/"), "/node_modules/")}
					result.MappedFrames++
					mapped = true
				}
			}
			if !mapped && f.InApp {
				result.UnmappedFrames++
			}
		}
		return frames
	}
	event.Frames = resolve(event.Frames)
	event.Causes = append([]api.IssueCause(nil), event.Causes...)
	for i := range event.Causes {
		event.Causes[i].Frames = resolve(event.Causes[i].Frames)
	}
	return event, result
}
