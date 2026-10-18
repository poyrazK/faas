package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type ProductNotificationScope struct{ ProductPlanScope }

type ProductNotificationStore interface {
	GetProductNotification(context.Context, ProductNotificationScope, string) (api.ProductNotification, error)
	ListProductNotifications(context.Context, ProductNotificationScope, string, string) (api.ProductNotificationPage, error)
	UpdateProductNotification(context.Context, ProductNotificationScope, api.ProductNotificationUpdate) (api.ProductNotification, error)
}

type productNotificationSource struct {
	value   api.ProductNotification
	version string
}

type productNotificationRead struct {
	sourceVersion string
	read          bool
	version       int64
	expires       time.Time
}

type productNotificationCursor struct {
	Scope string    `json:"scope"`
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
}

func notificationCursor(s ProductNotificationScope, filter, raw string) (productNotificationCursor, error) {
	c := productNotificationCursor{Scope: productOwnerHash([]string{"notification/v1", s.AccountID, s.AppID, s.TenantID, s.AuthAppID, s.OrganizationID, filter})}
	if filter != "all" && filter != "unread" {
		return c, ErrInvalidArgument
	}
	if raw == "" {
		return c, nil
	}
	if len(raw) > api.ProductControlOwnerCursorMaxBytes {
		return c, ErrInvalidArgument
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return c, ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var out productNotificationCursor
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.Scope != c.Scope || out.At.IsZero() {
		return c, ErrInvalidArgument
	}
	if _, _, ok := productcontrol.NotificationReference(out.ID); !ok {
		return c, ErrInvalidArgument
	}
	return out, nil
}

func notificationPage(s ProductNotificationScope, now time.Time) api.ProductNotificationPage {
	return api.ProductNotificationPage{AccountID: s.AccountID, AppID: s.AppID, TenantID: s.TenantID, Items: []api.ProductNotification{}, ObservedAt: now, WindowStart: now.Add(-api.ProductControlNotificationWindow), ReadStateScope: "workspace"}
}

func notificationSource(kind, id, feature, helpKind, version string, at time.Time) productNotificationSource {
	source := "work"
	if helpKind != "" {
		source = "help"
	}
	return productNotificationSource{value: api.ProductNotification{ID: source + "/" + id, ReferenceID: id, Kind: kind, Feature: feature, HelpKind: helpKind, OccurredAt: at.UTC().Truncate(time.Microsecond)}, version: version}
}

func notificationWorkVersion(state string, at time.Time) string {
	return "work:" + state + ":" + strconv.FormatInt(at.UnixMicro(), 10)
}

func notificationProjection(s ProductNotificationScope, source productNotificationSource, read productNotificationRead, now time.Time) api.ProductNotification {
	n := source.value
	n.Revision = productOwnerHash([]string{"notification/v1", s.AccountID, s.AppID, s.TenantID, s.AuthAppID, s.OrganizationID, n.ID, source.version})
	if read.expires.After(now) {
		n.ReadVersion = read.version
		n.Read = read.read && read.sourceVersion == source.version
	}
	return n
}

func notificationUpdate(current api.ProductNotification, req api.ProductNotificationUpdate) (api.ProductNotification, bool, error) {
	if req.Revision != current.Revision {
		return current, false, ErrConflict
	}
	if current.Read == *req.Read {
		return current, false, nil
	}
	if current.ReadVersion != *req.ExpectedVersion || current.ReadVersion >= api.ProductControlNotificationMaxUpdates {
		return current, false, ErrConflict
	}
	current.Read = *req.Read
	current.ReadVersion++
	return current, true, nil
}

func notificationFinish(page api.ProductNotificationPage, cursor productNotificationCursor) api.ProductNotificationPage {
	if len(page.Items) > api.ProductControlNotificationPageSize {
		page.Items = page.Items[:api.ProductControlNotificationPageSize]
		last := page.Items[len(page.Items)-1]
		cursor.ID, cursor.At = last.ID, last.OccurredAt
		raw, _ := json.Marshal(cursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page
}
