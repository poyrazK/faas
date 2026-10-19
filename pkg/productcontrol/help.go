package productcontrol

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func HelpVisible(kind string, p api.ProductPermissions) bool {
	switch kind {
	case "workspace":
		return p != (api.ProductPermissions{})
	case "feature":
		return p.RunWork || p.ViewBilling
	case "run":
		return p.ViewWork
	}
	return false
}

func HelpStatus(status string) bool {
	return status == "open" || status == "in_progress" || status == "resolved"
}

func HelpText(text string) bool {
	if !utf8.ValidString(text) || len(text) > api.ProductControlHelpTextMaxBytes || strings.TrimSpace(text) == "" {
		return false
	}
	return !strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' })
}

func ValidateHelpCreate(r api.ProductHelpCreate) error {
	id, err := uuid.Parse(r.ID)
	if err != nil || id == uuid.Nil || id.String() != r.ID || !HelpText(r.Message) {
		return fmt.Errorf("help requires a canonical request ID and bounded message")
	}
	switch r.Kind {
	case "workspace":
		if r.Feature == "" && r.OperationID == "" {
			return nil
		}
	case "feature":
		if slug.MatchString(r.Feature) && r.OperationID == "" {
			return nil
		}
	case "run":
		op, e := uuid.Parse(r.OperationID)
		if e == nil && op != uuid.Nil && op.String() == r.OperationID && r.Feature == "" {
			return nil
		}
	}
	return fmt.Errorf("help kind requires its matching feature or run reference")
}

func ValidateHelpUpdate(u api.ProductHelpUpdate) error {
	if u.ExpectedVersion < 1 || u.ExpectedVersion > api.ProductControlHelpMaxUpdates || !HelpStatus(u.Status) || !HelpText(u.Response) {
		return fmt.Errorf("help update requires a current version, status and bounded customer-visible response")
	}
	return nil
}
