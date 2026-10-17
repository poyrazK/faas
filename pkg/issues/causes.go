package issues

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"unicode/utf8"
)

func normalizeCauses(in *api.IssueEvent, sanitize func(string, int) string) error {
	if len(in.Causes) > api.IssueMaxCauses {
		return errors.New("cause count exceeds the limit")
	}
	in.Causes = append([]api.IssueCause(nil), in.Causes...)
	for i := range in.Causes {
		c := &in.Causes[i]
		if strings.TrimSpace(c.ExceptionType) == "" || len(c.ExceptionType) > api.IssueMaxTypeBytes || len(c.ErrorCode) > api.IssueMaxTypeBytes || !utf8.ValidString(c.ExceptionType) || !utf8.ValidString(c.ErrorCode) || len(c.Frames) > api.IssueMaxFrames {
			return errors.New("invalid cause evidence")
		}
		if c.FrameOrder != "" && c.FrameOrder != "newest-first" && c.FrameOrder != "oldest-first" {
			return errors.New("invalid cause frame_order")
		}
		c.ExceptionType = sanitize(strings.TrimSpace(c.ExceptionType), api.IssueMaxTypeBytes)
		c.Message = sanitize(c.Message, api.IssueMessageMaxBytes)
		c.ErrorCode = sanitize(strings.TrimSpace(c.ErrorCode), api.IssueMaxTypeBytes)
		c.Frames = append([]api.IssueFrame(nil), c.Frames...)
		for j := range c.Frames {
			f := &c.Frames[j]
			if f.Line < 0 || f.Column < 0 || f.Column > api.IssueFrameCoordinateMax || !utf8.ValidString(f.File) || !utf8.ValidString(f.Function) {
				return errors.New("invalid cause stack frame")
			}
			f.File = sanitize(f.File, api.IssueMaxFrameBytes)
			f.Function = sanitize(f.Function, api.IssueMaxFrameBytes)
		}
	}
	return nil
}
