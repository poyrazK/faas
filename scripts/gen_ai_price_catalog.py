#!/usr/bin/env python3
"""Generate pkg/outbound/aiprices/catalog.json (ADR-994).

Input is LiteLLM's model_prices_and_context_window.json (MIT, Copyright (c)
2023 Berri AI; see pkg/outbound/aiprices/NOTICE) at a pinned commit. The
output keeps the chat, completion, responses, embedding, image generation,
transcription and speech models of the providers the AI gateway speaks to
(ADR-994, ADR-997), converted from USD to EUR millicents at a recorded ECB
reference rate and rounded up, so a budget never under-counts:

- token prices (input, cached input, cache write, output, input and output
  image tokens) per million tokens;
- image prices per image;
- audio prices per minute of input audio;
- character prices per million input characters;
- video prices per second of generated video (ADR-1432), for the providers
  whose video calls the gateway meters, with a "<model>@<resolution>" row
  for each resolution LiteLLM prices apart;
- built-in tool prices (ADR-1433): per web search from LiteLLM's
  search_context_cost_per_query (the dearest context size), and for
  OpenAI and Azure OpenAI models the file search price and the price per GB
  code-interpreter container session.

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
            "vertex_ai-video-models",
        ),
        ("vertex_ai/",),
    ),
}
# Realtime models are priced per token like chat (ADR 20261019072722).
MODES = {"chat", "completion", "responses", "embedding", "image_generation", "audio_transcription", "audio_speech", "realtime"}
AUDIO_MODES = {"audio_transcription", "audio_speech"}
# Video generation is metered for Veo on Gemini and Vertex (ADR-1432).
VIDEO_MODE = "video_generation"
VIDEO_PROVIDERS = {"gemini", "vertex"}
VIDEO_TIER_PREFIX = "output_cost_per_second_"
# Built-in tools (ADR-1433). File search has no LiteLLM model entry: OpenAI's
# pricing page lists $2.50 per 1,000 calls (read 2026-10-16). The container
# price per GB session is LiteLLM's openai/container
# code_interpreter_cost_per_session, which prices a 1 GB container.
OPENAI_TOOL_PROVIDERS = {"openai", "azure_openai"}
FILE_SEARCH_USD_PER_CALL = "0.0025"
CONTAINER_ENTRY = "openai/container"
MAX_PRICE = 1_000_000_000  # api.MaxOutboundAIPriceMillicentsPerMTok
MAX_UNIT_PRICE = 10_000_000  # api.MaxOutboundAIPriceMillicentsPerImage, ...PerAudioMinute and ...PerVideoSecond
MILLICENTS_PER_USD = Decimal(10) ** 5
MAX_MODEL_BYTES = 128  # api.OutboundAIModelNameMaxBytes
MILLICENTS_PER_MTOK_PER_USD_PER_TOKEN = Decimal(10) ** 11


def convert(usd_per_token, usd_per_eur):
    value = (Decimal(usd_per_token) * MILLICENTS_PER_MTOK_PER_USD_PER_TOKEN / usd_per_eur).to_integral_value(ROUND_CEILING)
    return int(value)


def convert_unit(usd_per_unit, usd_per_eur):
    value = (Decimal(usd_per_unit) * MILLICENTS_PER_USD / usd_per_eur).to_integral_value(ROUND_CEILING)
    return int(value)


def optional(entry, field, convert_fn, usd_per_eur, scale=1):
    value = entry.get(field)
    if value is None or isinstance(value, (dict, list)):
        return None
    return convert_fn(Decimal(value) * scale, usd_per_eur)


def media_prices(entry, usd_per_eur):
    """input_image, output_image, image, audio_minute, characters (ADR-997), video_second (ADR-1432)."""
    if entry.get("mode") == VIDEO_MODE:
        return [None, None, None, None, None, video_price(entry, usd_per_eur)]
    return [
        optional(entry, "input_cost_per_image_token", convert, usd_per_eur),
        optional(entry, "output_cost_per_image_token", convert, usd_per_eur),
        optional(entry, "output_cost_per_image", convert_unit, usd_per_eur),
        optional(entry, "input_cost_per_second", convert_unit, usd_per_eur, 60),
        optional(entry, "input_cost_per_character", convert, usd_per_eur),
        None,
    ]


def video_price(entry, usd_per_eur):
    """A video model's price per second at its default resolution."""
    for field in ("output_cost_per_video_per_second", "output_cost_per_second"):
        price = optional(entry, field, convert_unit, usd_per_eur)
        if price is not None:
            return price
    return None


def video_tiers(entry, usd_per_eur):
    """Resolution -> price per second, for resolutions priced apart."""
    tiers = {}
    for field, value in entry.items():
        if field.startswith(VIDEO_TIER_PREFIX) and value is not None and not isinstance(value, (dict, list)):
            resolution = field[len(VIDEO_TIER_PREFIX):].lower()
            if resolution.isascii() and resolution.isalnum():
                tiers[resolution] = convert_unit(Decimal(value), usd_per_eur)
    return tiers


def tool_prices(entry, gateway, usd_per_eur, container_usd):
    """web_search, file_search, code_session_gb (ADR-1433)."""
    web_search = None
    search = entry.get("search_context_cost_per_query")
    if isinstance(search, dict):
        values = [Decimal(value) for value in search.values() if value is not None and not isinstance(value, (dict, list))]
        if values and max(values) > 0:
            web_search = convert_unit(max(values), usd_per_eur)
    if gateway not in OPENAI_TOOL_PROVIDERS or entry.get("mode") not in {"chat", "responses"}:
        return [web_search, None, None]
    file_search = convert_unit(Decimal(FILE_SEARCH_USD_PER_CALL), usd_per_eur)
    code_session = None if container_usd is None else convert_unit(container_usd, usd_per_eur)
    return [web_search, file_search, code_session]


def audio_prices(entry, usd_per_eur):
    """input_audio, cached_input_audio, output_audio per million audio tokens (ADR 20261019072722)."""
    return [
        optional(entry, "input_cost_per_audio_token", convert, usd_per_eur),
        optional(entry, "cache_read_input_audio_token_cost", convert, usd_per_eur),
        optional(entry, "output_cost_per_audio_token", convert, usd_per_eur),
    ]


def within_limits(row):
    limits = [MAX_PRICE] * 6 + [MAX_UNIT_PRICE, MAX_UNIT_PRICE, MAX_PRICE, MAX_UNIT_PRICE] + [MAX_UNIT_PRICE] * 3 + [MAX_PRICE] * 3
    return all(value is None or 0 <= value <= limit for value, limit in zip(row, limits))


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
    container = data.get(CONTAINER_ENTRY, {}).get("code_interpreter_cost_per_session")
    container_usd = None if container is None else Decimal(container)
    providers = {}
    for gateway, (sources, prefixes) in PROVIDERS.items():
        models = {}
        for key, entry in data.items():
            if not isinstance(entry, dict) or entry.get("litellm_provider") not in sources:
                continue
            video = entry.get("mode") == VIDEO_MODE
            if (video and gateway not in VIDEO_PROVIDERS) or (not video and entry.get("mode") not in MODES):
                continue
            name = model_name(key, prefixes)
            if name is None:
                continue
            media = media_prices(entry, usd_per_eur)
            token_input = entry.get("input_cost_per_token")
            if token_input is None and not any(media):
                continue
            # Audio models bill audio tokens as input; the dearer of the two
            # input prices keeps a cap from under-counting.
            if entry.get("mode") in AUDIO_MODES and entry.get("input_cost_per_audio_token") is not None:
                token_input = max(Decimal(token_input or 0), Decimal(entry["input_cost_per_audio_token"]))
            input_price = 0 if token_input is None else convert(token_input, usd_per_eur)
            cached = entry.get("cache_read_input_token_cost")
            write = entry.get("cache_creation_input_token_cost")
            output = entry.get("output_cost_per_token")
            row = [
                input_price,
                input_price if cached is None else convert(cached, usd_per_eur),
                input_price if write is None else convert(write, usd_per_eur),
                0 if output is None else convert(output, usd_per_eur),
            ] + media + tool_prices(entry, gateway, usd_per_eur, container_usd) + audio_prices(entry, usd_per_eur)
            # Free experimental entries say nothing about spend; without a
            # row the call stays unpriced exactly as before the catalog.
            if not within_limits(row) or not any(row):
                continue
            # The same model under two LiteLLM providers keeps the dearer
            # entry: a spend cap must not under-count.
            if name not in models or price_weight(row) > price_weight(models[name]):
                models[name] = row
            if video:
                for resolution, price in video_tiers(entry, usd_per_eur).items():
                    tier_name = name + "@" + resolution
                    tier_row = [0, 0, 0, 0, None, None, None, None, None, price, None, None, None, None, None, None]
                    if len(tier_name) <= MAX_MODEL_BYTES and within_limits(tier_row) and (
                            tier_name not in models or price_weight(tier_row) > price_weight(models[tier_name])):
                        models[tier_name] = tier_row
        providers[gateway] = dict(sorted(models.items()))
    return providers


def price_weight(row):
    return sum(value for value in row if value is not None)


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
        "unit": "EUR millicents, rounded up: per million tokens or characters, per image, per audio minute, per second of video, "
                "per web search, per file search, per GB container session, per million audio tokens",
        "fields": ["input", "cached_input", "cache_write", "output", "input_image", "output_image", "image", "audio_minute", "characters",
                   "video_second", "web_search", "file_search", "code_session_gb", "input_audio", "cached_input_audio", "output_audio"],
    }
    with open(args.output, "w", encoding="utf-8") as handle:
        handle.write(render(meta, build(data, usd_per_eur)))


if __name__ == "__main__":
    main()
