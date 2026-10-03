package state

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func EncodeIssueCursor(c IssueCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func DecodeIssueCursor(value string) (IssueCursor, error) {
	if value == "" {
		return IssueCursor{}, nil
	}
	var c IssueCursor
	if len(value) > api.IssueCursorMaxBytes {
		return c, ErrInvalidArgument
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return c, ErrInvalidArgument
	}
	if err = json.Unmarshal(b, &c); err != nil || c.Time.IsZero() || c.Time.After(time.Now().Add(api.IssueMaxClockSkew)) {
		return c, ErrInvalidArgument
	}
	if _, err = uuid.Parse(c.ID); err != nil {
		return c, ErrInvalidArgument
	}
	if c.MinCustomers < 0 || c.ImpactCustomers < 0 {
		return c, ErrInvalidArgument
	}
	switch c.Sort {
	case "":
		if c.MinCustomers != 0 || c.ImpactCustomers != 0 || c.ImpactWindowEnd != nil {
			return c, ErrInvalidArgument
		}
	case "recent":
		if c.MinCustomers <= 0 || c.ImpactCustomers != 0 || !validIssueImpactCursorWindow(c.ImpactWindowEnd) {
			return c, ErrInvalidArgument
		}
	case "impact":
		if !validIssueImpactCursorWindow(c.ImpactWindowEnd) {
			return c, ErrInvalidArgument
		}
	default:
		return c, ErrInvalidArgument
	}
	return c, nil
}

func validIssueImpactCursorWindow(windowEnd *time.Time) bool {
	return windowEnd != nil && !windowEnd.IsZero() && !windowEnd.After(time.Now().Add(api.IssueMaxClockSkew))
}

// ValidateIssueListCursor prevents a cursor from being reused with a
// different issue ordering or customer-impact threshold.
func ValidateIssueListCursor(filter IssueListFilter, cursor IssueCursor) error {
	sortBy := filter.Sort
	if sortBy == "" {
		sortBy = "recent"
	}
	if (sortBy != "recent" && sortBy != "impact") || filter.MinCustomers < 0 {
		return ErrInvalidArgument
	}
	usesImpactQuery := sortBy == "impact" || filter.MinCustomers > 0
	if cursor.ID == "" {
		if cursor.Time.IsZero() && cursor.Sort == "" && cursor.MinCustomers == 0 && cursor.ImpactCustomers == 0 && cursor.ImpactWindowEnd == nil {
			return nil
		}
		return ErrInvalidArgument
	}
	if usesImpactQuery {
		if cursor.Sort != sortBy || cursor.MinCustomers != filter.MinCustomers || !validIssueImpactCursorWindow(cursor.ImpactWindowEnd) {
			return ErrInvalidArgument
		}
		if sortBy == "recent" && cursor.ImpactCustomers != 0 {
			return ErrInvalidArgument
		}
		return nil
	}
	if cursor.Sort != "" || cursor.MinCustomers != 0 || cursor.ImpactCustomers != 0 || cursor.ImpactWindowEnd != nil {
		return ErrInvalidArgument
	}
	return nil
}
