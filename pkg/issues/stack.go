package issues

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var nodeFrame = regexp.MustCompile(`^\s*at\s+(?:(.*?)\s+\()?(.+?):([0-9]+):([0-9]+)\)?$`)
var pythonFrame = regexp.MustCompile(`^\s*File "([^"]+)", line ([0-9]+), in (.*)$`)

// ParseStack supports the initial Node/Python capture contract. Unsupported
// stack representations retain sanitized evidence and use fallback grouping.
func ParseStack(stack string) []api.IssueFrame {
	frames, _ := parseStack(stack, false, false)
	return frames
}

func parseFailureSiteStack(stack string) ([]api.IssueFrame, string) {
	return parseStack(stack, true, false)
}

func parseStack(stack string, failureSite, columns bool) ([]api.IssueFrame, string) {
	out := []api.IssueFrame{}
	order := "newest-first"
	for _, line := range strings.Split(stack, "\n") {
		var f api.IssueFrame
		if m := nodeFrame.FindStringSubmatch(line); m != nil {
			f = api.IssueFrame{File: m[2], Function: m[1], InApp: !strings.HasPrefix(m[2], "node:") && !strings.Contains(m[2], "/node_modules/")}
			f.Line, _ = strconv.Atoi(m[3])
			if columns {
				var err error
				f.Column, err = strconv.Atoi(m[4])
				if err != nil {
					f.Column = -1
				}
			}
		} else if m := pythonFrame.FindStringSubmatch(line); m != nil {
			order = "oldest-first"
			f = api.IssueFrame{File: m[1], Function: m[3], InApp: !strings.Contains(m[1], "site-packages")}
			f.Line, _ = strconv.Atoi(m[2])
		} else {
			continue
		}
		out = append(out, f)
		if failureSite && order == "oldest-first" && len(out) > api.IssueMaxFrames {
			out = out[1:]
		} else if len(out) == api.IssueMaxFrames && (!failureSite || order == "newest-first") {
			break
		}
	}
	return out, order
}
