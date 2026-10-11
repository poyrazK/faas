package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

const routeMonitorCodeownersMaxBytes = 3 << 20

type routeMonitorSourceOwnership struct {
	Status            string                        `json:"status"`
	Reason            string                        `json:"reason,omitempty"`
	CodeownersFile    string                        `json:"codeowners_file,omitempty"`
	CandidateRevision string                        `json:"candidate_revision,omitempty"`
	EvidenceTruncated bool                          `json:"evidence_truncated,omitempty"`
	Files             []routeMonitorOwnedSourceFile `json:"files"`
}

const routeMonitorOwnershipCaveat = "CODEOWNERS attribution comes from the local candidate revision and is a handoff hint; it does not verify repository access, owner availability, or runtime responsibility."

type routeMonitorOwnedSourceFile struct {
	Path        string   `json:"path"`
	Role        string   `json:"role"`
	Change      string   `json:"change,omitempty"`
	Revision    string   `json:"revision,omitempty"`
	Line        int      `json:"line,omitempty"`
	Status      string   `json:"status"`
	Owners      []string `json:"owners"`
	RulePattern string   `json:"rule_pattern,omitempty"`
	RuleLine    int      `json:"rule_line,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

type routeMonitorCodeownersRule struct {
	pattern string
	owners  []string
	line    int
}

func routeMonitorIncidentCustomerImpact(incident api.RouteMonitorIncident, method, route string) (*api.RouteMonitorCustomerImpact, string) {
	report := incident.OpeningReport.Customers
	if report == nil {
		return nil, "not_configured"
	}
	want := previewSourceRouteKey(method, route)
	if want == "" {
		return nil, "unsupported_route_template"
	}
	var matched *api.RouteMonitorCustomerRoute
	for index := range report.Routes {
		item := &report.Routes[index]
		if previewSourceRouteKey(item.Method, item.Path) != want {
			continue
		}
		if matched != nil {
			return nil, "ambiguous"
		}
		matched = item
	}
	if matched == nil {
		return nil, "route_not_reported"
	}
	// Keep only aggregate counts; incident evidence can contain customer IDs.
	return &api.RouteMonitorCustomerImpact{
		GroupBy: report.GroupBy, Coverage: report.Coverage,
		ObservedCustomers: matched.ObservedCustomers, ViolatedCustomers: matched.ViolatedCustomers,
		UnknownCustomers: matched.UnknownCustomers,
	}, "available"
}

// addRouteMonitorSourceOwners only reads the local checkout. It resolves
// CODEOWNERS from the incident candidate commit so a dirty or newer local file
// cannot silently change the owner assignment for an older release.
func addRouteMonitorSourceOwners(ctx context.Context, startDirectory string, correlation *routeMonitorSourceCorrelation) {
	if correlation == nil || correlation.Status != "release_pair_bound" || len(correlation.Routes) == 0 {
		return
	}
	correlation.OwnershipCaveat = routeMonitorOwnershipCaveat
	root, err := localRouteMonitorSourcePath(ctx, startDirectory, ".")
	if err != nil {
		setRouteMonitorOwnershipUnavailable(correlation, "local_repository_unavailable")
		return
	}
	remote, err := runRouteMonitorOwnerGit(ctx, root, "config", "--get", "remote.origin.url")
	if err != nil {
		setRouteMonitorOwnershipUnavailable(correlation, "local_repository_unavailable")
		return
	}
	localRepository, _ := routeimpact.RepositoryReference(strings.TrimSpace(string(remote)))
	incidentRepository, _ := routeimpact.RepositoryReference(correlation.Repository)
	if localRepository == "" || incidentRepository == "" || !strings.EqualFold(localRepository, incidentRepository) {
		setRouteMonitorOwnershipUnavailable(correlation, "local_repository_mismatch")
		return
	}
	if !routeimpact.ValidCommit(correlation.CandidateRevision) {
		setRouteMonitorOwnershipUnavailable(correlation, "candidate_revision_unavailable")
		return
	}
	revision := strings.ToLower(correlation.CandidateRevision)
	if _, err := runRouteMonitorOwnerGit(ctx, root, "cat-file", "-e", revision+"^{commit}"); err != nil {
		setRouteMonitorOwnershipUnavailable(correlation, "candidate_revision_unavailable")
		return
	}
	contents, codeownersPath, reason := readRouteMonitorCodeowners(ctx, root, revision)
	if reason != "" {
		setRouteMonitorOwnershipUnavailable(correlation, reason)
		return
	}
	rules := parseRouteMonitorCodeowners(contents)
	for index := range correlation.Routes {
		route := &correlation.Routes[index]
		route.Ownership = mapRouteMonitorSourceOwnership(route.Source, correlation.SourceRoot, codeownersPath, revision, rules)
	}
}

func setRouteMonitorOwnershipUnavailable(correlation *routeMonitorSourceCorrelation, reason string) {
	for index := range correlation.Routes {
		files := routeMonitorSourceFiles(correlation.Routes[index].Source, correlation.SourceRoot)
		for fileIndex := range files {
			files[fileIndex].Status = "unavailable"
			files[fileIndex].Reason = reason
		}
		correlation.Routes[index].Ownership = &routeMonitorSourceOwnership{
			Status: "unavailable", Reason: reason,
			EvidenceTruncated: correlation.Routes[index].Source != nil && correlation.Routes[index].Source.EvidenceTruncated,
			Files:             files,
		}
	}
}

func readRouteMonitorCodeowners(ctx context.Context, root, revision string) ([]byte, string, string) {
	for _, candidate := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		body, err := runRouteMonitorOwnerGit(ctx, root, "cat-file", "blob", revision+":"+candidate)
		if err != nil {
			var tooLarge *routeMonitorOwnerOutputLimitError
			if errors.As(err, &tooLarge) {
				return nil, "", "codeowners_too_large"
			}
			continue
		}
		return body, candidate, ""
	}
	return nil, "", "codeowners_not_found"
}

func mapRouteMonitorSourceOwnership(source *routeInvestigationSourceRoute, sourceRoot, codeownersFile, revision string, rules []routeMonitorCodeownersRule) *routeMonitorSourceOwnership {
	files := routeMonitorSourceFiles(source, sourceRoot)
	ownership := &routeMonitorSourceOwnership{
		CodeownersFile: codeownersFile, CandidateRevision: revision, Files: files,
		EvidenceTruncated: source != nil && source.EvidenceTruncated,
	}
	if len(files) == 0 {
		ownership.Status, ownership.Reason = "unavailable", "source_evidence_unavailable"
		return ownership
	}
	for index := range ownership.Files {
		file := &ownership.Files[index]
		if file.Reason != "" {
			file.Status = "ambiguous"
			continue
		}
		matched := routeMonitorMatchCodeowners(rules, file.Path)
		if matched == nil {
			file.Status = "unowned"
			continue
		}
		file.RulePattern, file.RuleLine = matched.pattern, matched.line
		file.Owners = append([]string(nil), matched.owners...)
		if len(matched.owners) == 0 {
			file.Status = "unowned"
		} else {
			file.Status = "owned"
		}
	}
	ownership.Status = aggregateRouteMonitorOwnership(ownership.Files)
	if ownership.Status == "ambiguous" {
		ownership.Reason = "source_path_unavailable"
	}
	return ownership
}

func aggregateRouteMonitorOwnership(files []routeMonitorOwnedSourceFile) string {
	owned, unowned, ambiguous, unavailable := 0, 0, 0, 0
	for _, file := range files {
		switch file.Status {
		case "owned":
			owned++
		case "unowned":
			unowned++
		case "ambiguous":
			ambiguous++
		case "unavailable":
			unavailable++
		}
	}
	if ambiguous > 0 {
		return "ambiguous"
	}
	if unavailable > 0 {
		return "unavailable"
	}
	if owned > 0 && unowned > 0 {
		return "partially_owned"
	}
	if owned > 0 {
		return "owned"
	}
	if unowned > 0 {
		return "unowned"
	}
	return "unavailable"
}

func routeMonitorSourceFiles(source *routeInvestigationSourceRoute, sourceRoot string) []routeMonitorOwnedSourceFile {
	if source == nil {
		return nil
	}
	files := make([]routeMonitorOwnedSourceFile, 0, len(source.Evidence)*2+2)
	seen := map[string]struct{}{}
	add := func(file, role, change, revision string, line int) {
		if file == "" {
			return
		}
		pathValue, err := routeMonitorRepositoryPath(sourceRoot, file)
		key := role + "\x00" + pathValue + "\x00" + revision
		if err == nil {
			if _, ok := seen[key]; ok {
				return
			}
			seen[key] = struct{}{}
		}
		entry := routeMonitorOwnedSourceFile{Path: pathValue, Role: role, Change: change, Revision: revision, Line: line, Owners: []string{}}
		if err != nil {
			entry.Path = file
			entry.Reason = "unsafe_source_path"
		}
		files = append(files, entry)
	}
	for _, evidence := range source.Evidence {
		add(evidence.File, "changed_source", evidence.Change, evidence.Revision, evidence.Line)
		for _, symbol := range evidence.ViaSymbols {
			add(symbol.File, "reference_chain", evidence.Change, evidence.Revision, symbol.Line)
		}
	}
	if source.BeforeLocation != nil {
		add(source.BeforeLocation.File, "baseline_handler", "", "base", source.BeforeLocation.Line)
	}
	if source.AfterLocation != nil {
		add(source.AfterLocation.File, "candidate_handler", "", "candidate", source.AfterLocation.Line)
	}
	return files
}

func routeMonitorRepositoryPath(sourceRoot, file string) (string, error) {
	if strings.ContainsAny(file, "\\\x00\r\n") || strings.HasPrefix(file, "/") {
		return "", errors.New("invalid source path")
	}
	cleanFile := path.Clean(file)
	if cleanFile == "." || cleanFile == ".." || strings.HasPrefix(cleanFile, "../") || cleanFile != file {
		return "", errors.New("invalid source path")
	}
	root := sourceRoot
	if root == "" || root == "." {
		root = ""
	} else {
		cleanRoot := path.Clean(root)
		if strings.HasPrefix(root, "/") || cleanRoot == ".." || strings.HasPrefix(cleanRoot, "../") || cleanRoot != root || strings.ContainsAny(root, "\\\x00\r\n") {
			return "", errors.New("invalid source root")
		}
		root = cleanRoot
	}
	joined := path.Join(root, cleanFile)
	if joined == ".." || strings.HasPrefix(joined, "../") || strings.HasPrefix(joined, "/") {
		return "", errors.New("invalid source path")
	}
	return joined, nil
}

func routeMonitorMatchCodeowners(rules []routeMonitorCodeownersRule, repositoryPath string) *routeMonitorCodeownersRule {
	var matched *routeMonitorCodeownersRule
	for index := range rules {
		if routeMonitorCodeownersPatternMatches(rules[index].pattern, repositoryPath) {
			matched = &rules[index]
		}
	}
	return matched
}

func parseRouteMonitorCodeowners(contents []byte) []routeMonitorCodeownersRule {
	rules := make([]routeMonitorCodeownersRule, 0)
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 4096), routeMonitorCodeownersMaxBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "\\#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pattern := fields[0]
		if !routeMonitorCodeownersPatternSupported(pattern) {
			continue
		}
		owners := make([]string, 0, len(fields)-1)
		valid := true
		for _, owner := range fields[1:] {
			if strings.HasPrefix(owner, "#") {
				break
			}
			if !routeMonitorCodeownersOwnerSupported(owner) {
				valid = false
				break
			}
			owners = append(owners, owner)
		}
		if valid {
			rules = append(rules, routeMonitorCodeownersRule{pattern: pattern, owners: owners, line: lineNumber})
		}
	}
	return rules
}

func routeMonitorCodeownersOwnerSupported(owner string) bool {
	if owner == "" || strings.ContainsAny(owner, "\\\x00\r\n") {
		return false
	}
	if strings.HasPrefix(owner, "@") {
		return len(owner) > 1 && !strings.ContainsAny(owner, "[]!#")
	}
	return strings.Count(owner, "@") == 1 && !strings.ContainsAny(owner, "[]!#")
}

func routeMonitorCodeownersPatternSupported(pattern string) bool {
	if pattern == "" || pattern == "!" || strings.HasPrefix(pattern, "!") || strings.ContainsAny(pattern, "[]\x00\r\n") {
		return false
	}
	trimmed := strings.TrimPrefix(pattern, "/")
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return false
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		if _, err := path.Match(segment, ""); err != nil {
			return false
		}
	}
	return true
}

func routeMonitorCodeownersPatternMatches(pattern, repositoryPath string) bool {
	if !routeMonitorCodeownersPatternSupported(pattern) || repositoryPath == "" || strings.HasPrefix(repositoryPath, "/") {
		return false
	}
	rootAnchored := strings.HasPrefix(pattern, "/")
	trailingDirectory := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	pathSegments := strings.Split(repositoryPath, "/")
	patternSegments := strings.Split(pattern, "/")
	if len(patternSegments) == 1 {
		if rootAnchored {
			if trailingDirectory {
				return len(pathSegments) > 1 && routeMonitorCodeownersSegmentMatches(patternSegments[0], pathSegments[0])
			}
			return len(pathSegments) == 1 && routeMonitorCodeownersSegmentMatches(patternSegments[0], pathSegments[0])
		}
		limit := len(pathSegments)
		if trailingDirectory {
			limit--
		}
		for index := 0; index < limit; index++ {
			if routeMonitorCodeownersSegmentMatches(patternSegments[0], pathSegments[index]) {
				return true
			}
		}
		return false
	}
	if !trailingDirectory && !strings.Contains(pattern, "**") {
		return routeMonitorCodeownersSegmentsMatch(patternSegments, pathSegments)
	}
	limit := len(pathSegments)
	if trailingDirectory {
		limit--
	}
	for end := 1; end <= limit; end++ {
		if routeMonitorCodeownersSegmentsMatch(patternSegments, pathSegments[:end]) {
			return true
		}
	}
	return false
}

func routeMonitorCodeownersSegmentsMatch(pattern, value []string) bool {
	if len(pattern) == 0 {
		return len(value) == 0
	}
	if pattern[0] == "**" {
		for consumed := 0; consumed <= len(value); consumed++ {
			if routeMonitorCodeownersSegmentsMatch(pattern[1:], value[consumed:]) {
				return true
			}
		}
		return false
	}
	if len(value) == 0 || !routeMonitorCodeownersSegmentMatches(pattern[0], value[0]) {
		return false
	}
	return routeMonitorCodeownersSegmentsMatch(pattern[1:], value[1:])
}

func routeMonitorCodeownersSegmentMatches(pattern, value string) bool {
	matched, err := path.Match(pattern, value)
	return err == nil && matched
}

func renderRouteMonitorSourceOwnership(ownership routeMonitorSourceOwnership) {
	_, _ = fmt.Fprintf(osStdout, "    Ownership: %s", ownership.Status)
	if ownership.Reason != "" {
		_, _ = fmt.Fprintf(osStdout, " (%s)", previewReportText(ownership.Reason))
	}
	if ownership.CodeownersFile != "" {
		_, _ = fmt.Fprintf(osStdout, "; %s at %s", previewReportText(ownership.CodeownersFile), previewReportText(ownership.CandidateRevision))
	}
	if ownership.EvidenceTruncated {
		_, _ = fmt.Fprint(osStdout, "; source evidence was truncated, so only listed files have owner attribution")
	}
	_, _ = fmt.Fprintln(osStdout)
	for _, file := range ownership.Files {
		_, _ = fmt.Fprintf(osStdout, "      %s %s — %s", file.Role, previewReportText(file.Path), file.Status)
		if file.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, " (%s)", previewReportText(file.Reason))
		}
		if len(file.Owners) > 0 {
			owners := make([]string, 0, len(file.Owners))
			for _, owner := range file.Owners {
				owners = append(owners, previewReportText(owner))
			}
			_, _ = fmt.Fprintf(osStdout, "; owners %s", strings.Join(owners, ", "))
		}
		if file.RulePattern != "" {
			_, _ = fmt.Fprintf(osStdout, "; rule %q at line %d", previewReportText(file.RulePattern), file.RuleLine)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
}

func runRouteMonitorOwnerGit(ctx context.Context, directory string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	command.Stderr = io.Discard
	output := &routeMonitorOwnerOutput{limit: routeMonitorCodeownersMaxBytes}
	command.Stdout = output
	if err := command.Run(); err != nil {
		if output.tooLarge {
			return nil, &routeMonitorOwnerOutputLimitError{}
		}
		return nil, err
	}
	return output.Bytes(), nil
}

type routeMonitorOwnerOutput struct {
	bytes.Buffer
	limit    int
	tooLarge bool
}

func (output *routeMonitorOwnerOutput) Write(body []byte) (int, error) {
	if len(body) > output.limit-output.Len() {
		output.tooLarge = true
		return 0, errors.New("CODEOWNERS exceeds size limit")
	}
	return output.Buffer.Write(body)
}

type routeMonitorOwnerOutputLimitError struct{}

func (*routeMonitorOwnerOutputLimitError) Error() string { return "CODEOWNERS exceeds size limit" }
