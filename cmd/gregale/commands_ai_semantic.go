package main

import (
	"errors"
	"flag"
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// semanticCacheFlags are ai policy's semantic cache flags (ADR 20261112194947).
type semanticCacheFlags struct {
	state, threshold, integration, model, shareUsers *string
}

func addSemanticCacheFlags(fs *flag.FlagSet) semanticCacheFlags {
	return semanticCacheFlags{
		state:       fs.String("semantic-cache", "", "answer calls that mean the same as a cached one: on or off"),
		threshold:   fs.String("semantic-threshold", "", "cosine similarity a semantic hit needs, 0.85 to 0.99"),
		integration: fs.String("semantic-embedding-integration", "", "the OpenAI or Gemini integration that embeds calls"),
		model:       fs.String("semantic-embedding-model", "", "the embedding model"),
		shareUsers:  fs.String("semantic-share-users", "", "let different end users share answers: on or off"),
	}
}

// apply edits the request's semantic cache settings, keeping what a flag
// leaves out.
func (f semanticCacheFlags) apply(req *api.PutOutboundAIPolicyRequest) error {
	edited := *f.threshold != "" || *f.integration != "" || *f.model != "" || *f.shareUsers != ""
	if *f.state == "" && !edited {
		return nil
	}
	semantic := api.OutboundAISemanticCache{}
	if req.SemanticCache != nil {
		semantic = *req.SemanticCache
	}
	switch *f.state {
	case "":
	case "on":
		semantic.Enabled = true
	case "off":
		if edited {
			return errors.New("--semantic-cache off turns the semantic cache off; drop the other --semantic flags")
		}
		req.SemanticCache = nil
		return nil
	default:
		return errors.New("--semantic-cache takes on or off")
	}
	if *f.threshold != "" {
		value, err := strconv.ParseFloat(*f.threshold, 64)
		bp := int(value*10_000 + 0.5)
		if err != nil || bp < api.OutboundAISemanticCacheThresholdMinBP || bp > api.OutboundAISemanticCacheThresholdMaxBP {
			return fmt.Errorf("--semantic-threshold %q must be 0.85 to 0.99", *f.threshold)
		}
		semantic.ThresholdBasisPoints = bp
	}
	if *f.integration != "" {
		semantic.EmbeddingIntegrationID = *f.integration
	}
	if *f.model != "" {
		semantic.EmbeddingModel = *f.model
	}
	switch *f.shareUsers {
	case "":
	case "on":
		semantic.ShareAcrossUsers = true
	case "off":
		semantic.ShareAcrossUsers = false
	default:
		return errors.New("--semantic-share-users takes on or off")
	}
	req.SemanticCache = &semantic
	return nil
}
