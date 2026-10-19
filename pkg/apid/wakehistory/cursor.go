package wakehistory

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var errSource = errors.New("invalid wake history source")

type cursor struct {
	Version int    `json:"v"`
	Account string `json:"account"`
	App     string `json:"app"`
	Since   string `json:"since"`
	Until   string `json:"until"`
	Limit   int    `json:"limit"`
	At      string `json:"at"`
	ID      string `json:"id"`
}

func parseTime(raw string) (time.Time, error) {
	if len(raw) > api.MCPWakeHistoryTimeMaxBytes {
		return time.Time{}, errSource
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	t = t.UTC().Truncate(time.Microsecond)
	if err != nil || t.IsZero() {
		return time.Time{}, errSource
	}
	return t, nil
}
func encode(c cursor, r state.WakeHistoryRecord) (string, error) {
	c.At, c.ID = r.StartedAt.UTC().Format(time.RFC3339Nano), r.InstanceID
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	raw := "wh1." + base64.RawURLEncoding.EncodeToString(b)
	if len(raw) > api.WakeHistoryCursorMaxBytes {
		return "", errSource
	}
	return raw, nil
}
func decode(raw string, want cursor) (state.WakeHistoryPosition, error) {
	fail := func() (state.WakeHistoryPosition, error) { return state.WakeHistoryPosition{}, errSource }
	s, ok := strings.CutPrefix(raw, "wh1.")
	if !ok || len(raw) > api.WakeHistoryCursorMaxBytes {
		return fail()
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil {
		return fail()
	}
	var c cursor
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != want.Version || c.Account != want.Account || c.App != want.App || c.Since != want.Since || c.Until != want.Until || c.Limit != want.Limit {
		return fail()
	}
	at, e := parseTime(c.At)
	id, ie := uuid.Parse(c.ID)
	if e != nil || ie != nil || id.String() != c.ID || at.Format(time.RFC3339Nano) != c.At {
		return fail()
	}
	return state.WakeHistoryPosition{At: at, ID: c.ID}, nil
}
