#!/usr/bin/env python3
"""Generate pkg/outbound/aiprices/catalog.json (ADR-992).

Input is LiteLLM's model_prices_and_context_window.json (MIT, Copyright (c)
2023 Berri AI; see pkg/outbound/aiprices/NOTICE) at a pinned commit. The
output keeps the token-priced chat, completion, responses and embedding
models of the providers the AI gateway speaks to, converted from USD per
token to EUR millicents per million tokens at a recorded ECB reference rate
and rounded up, so a budget never under-counts.

Run it with an isolated interpreter on a downloaded file kept in its own
directory, for example:

  python3 -I scripts/gen_ai_price_catalog.py \
      --input /tmp/litellm/model_prices_and_context_window.json \
      --commit <litellm commit sha> --usd-per-eur 1.1206 --fx-date 2026-10-09 \
      --generated 2026-10-12 --output pkg/outbound/aiprices/catalog.json
"""

import argparse
import json
from decimal import ROUND_CEILING, Decimal

# Gateway provider -> (LiteLLM providers, key prefixes to strip).
PROVIDERS = {
    "openai": (("openai",), ("openai/",)),
    "anthropic": (("anthropic",), ("anthropic/",)),
    "gemini": (("gemini",), ("gemini/",)),
    "azure_openai": (("azure",), ("azure/",)),
    "bedrock": (("bedrock", "bedrock_converse"), ("bedrock/", "converse/")),
    "vertex": (
        (
            "vertex_ai",
            "vertex_ai-language-models",
            "vertex_ai-anthropic_models",
            "vertex_ai-embedding-models",
            "vertex_ai-mistral_models",
            "vertex_ai-llama_models",
            "vertex_ai-ai21_models",
            "vertex_ai-deepseek_models",
            "vertex_ai-qwen_models",
            "vertex_ai-openai_models",
        ),
        ("vertex_ai/",),
    ),
}
MODES = {"chat", "completion", "responses", "embedding"}
MAX_PRICE = 1_000_000_000  # api.MaxOutboundAIPriceMillicentsPerMTok
MAX_MODEL_BYTES = 128  # api.OutboundAIModelNameMaxBytes
MILLICENTS_PER_MTOK_PER_USD_PER_TOKEN = Decimal(10) ** 11


def convert(usd_per_token, usd_per_eur):
    value = (Decimal(usd_per_token) * MILLICENTS_PER_MTOK_PER_USD_PER_TOKEN / usd_per_eur).to_integral_value(ROUND_CEILING)
    return int(value)


def model_name(key, prefixes):
    for prefix in prefixes:
        if key.startswith(prefix):
            key = key[len(prefix):]
            break
    # Region- or tier-scoped variants ("azure/eu/...", "bedrock/us-east-1/...")
    # are not model names a provider reports.
    if not key or "/" in key or len(key) > MAX_MODEL_BYTES or not key.isascii() or not key.isprintable():
        return None
    return key


def build(data, usd_per_eur):
    providers = {}
    for gateway, (sources, prefixes) in PROVIDERS.items():
        models = {}
        for key, entry in data.items():
            if not isinstance(entry, dict) or entry.get("litellm_provider") not in sources or entry.get("mode") not in MODES:
                continue
            name = model_name(key, prefixes)
            if name is None or entry.get("input_cost_per_token") is None:
                continue
            input_price = convert(entry["input_cost_per_token"], usd_per_eur)
            cached = entry.get("cache_read_input_token_cost")
            write = entry.get("cache_creation_input_token_cost")
            output = entry.get("output_cost_per_token")
            row = [
                input_price,
                input_price if cached is None else convert(cached, usd_per_eur),
                input_price if write is None else convert(write, usd_per_eur),
                0 if output is None else convert(output, usd_per_eur),
            ]
            # Free experimental entries say nothing about spend; without a
            # row the call stays unpriced exactly as before the catalog.
            if any(price < 0 or price > MAX_PRICE for price in row) or not any(row):
                continue
            # The same model under two LiteLLM providers keeps the dearer
            # entry: a spend cap must not under-count.
            if name not in models or sum(row) > sum(models[name]):
                models[name] = row
        providers[gateway] = dict(sorted(models.items()))
    return providers


def render(meta, providers):
    lines = ["{"]
    for key, value in meta.items():
        lines.append(f"  {json.dumps(key)}: {json.dumps(value)},")
    lines.append('  "providers": {')
    names = list(providers)
    for index, provider in enumerate(names):
        lines.append(f"    {json.dumps(provider)}: {{")
        models = list(providers[provider].items())
        for model_index, (model, row) in enumerate(models):
            comma = "," if model_index < len(models) - 1 else ""
            lines.append(f"      {json.dumps(model)}: {json.dumps(row)}{comma}")
        lines.append("    }" + ("," if index < len(names) - 1 else ""))
    lines.append("  }")
    lines.append("}")
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--input", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--usd-per-eur", required=True)
    parser.add_argument("--fx-date", required=True)
    parser.add_argument("--generated", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    usd_per_eur = Decimal(args.usd_per_eur)
    if usd_per_eur <= 0:
        raise SystemExit("--usd-per-eur must be positive")
    with open(args.input, encoding="utf-8") as handle:
        data = json.load(handle, parse_float=Decimal)
    meta = {
        "source": "https://github.com/BerriAI/litellm/blob/" + args.commit + "/model_prices_and_context_window.json",
        "license": "MIT, Copyright (c) 2023 Berri AI; see NOTICE",
        "generated": args.generated,
        "usd_per_eur": args.usd_per_eur,
        "fx_source": "ECB euro foreign exchange reference rate of " + args.fx_date,
        "unit": "EUR millicents per million tokens, rounded up",
        "fields": ["input", "cached_input", "cache_write", "output"],
    }
    with open(args.output, "w", encoding="utf-8") as handle:
        handle.write(render(meta, build(data, usd_per_eur)))


if __name__ == "__main__":
    main()
