package aiprices

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// max_input.json is generated beside catalog.json from the same LiteLLM
// commit (scripts/gen_ai_price_catalog.py --max-input): the context window,
// in input tokens, of each priced generation model.
//
//go:embed max_input.json
var maxInputJSON []byte

var defaultMaxInputs = sync.OnceValue(func() map[string]map[string]int64 {
	var file struct {
		Providers map[string]map[string]int64 `json:"providers"`
	}
	if err := json.Unmarshal(maxInputJSON, &file); err != nil {
		// Generated and covered by tests; a broken embed reserves the
		// gateway's fallback instead of failing calls.
		return nil
	}
	return file.Providers
})

// MaxInputTokens is the context window of the catalog model a call resolves
// to, as LiteLLM publishes it. The AI gateway reserves it for a call that
// continues context stored at the provider, whose body does not show that
// input (prod hunt 2026-11-28).
func (c *Catalog) MaxInputTokens(provider, model string) (int64, bool) {
	entry, ok := c.Lookup(provider, model)
	if !ok {
		return 0, false
	}
	limit, ok := defaultMaxInputs()[provider][entry.Model]
	return limit, ok && limit > 0
}
