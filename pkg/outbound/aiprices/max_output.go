package aiprices

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// max_output.json is generated beside catalog.json from the same LiteLLM
// commit (scripts/gen_ai_price_catalog.py --max-output): the longest
// response, in tokens, of each priced generation model.
//
//go:embed max_output.json
var maxOutputJSON []byte

var defaultMaxOutputs = sync.OnceValue(func() map[string]map[string]int64 {
	var file struct {
		Providers map[string]map[string]int64 `json:"providers"`
	}
	if err := json.Unmarshal(maxOutputJSON, &file); err != nil {
		// Generated and covered by tests; a broken embed reserves the
		// gateway's fallback instead of failing calls.
		return nil
	}
	return file.Providers
})

// MaxOutputTokens is the longest response the catalog model a call resolves
// to can return, as LiteLLM publishes it. The AI gateway reserves it when a
// call names no output ceiling, so concurrent calls cannot settle far past
// a hard cap (cost-control review 2026-11-19, N17).
func (c *Catalog) MaxOutputTokens(provider, model string) (int64, bool) {
	entry, ok := c.Lookup(provider, model)
	if !ok {
		return 0, false
	}
	limit, ok := defaultMaxOutputs()[provider][entry.Model]
	return limit, ok && limit > 0
}
