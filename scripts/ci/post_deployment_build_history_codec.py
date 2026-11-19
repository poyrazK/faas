"""Bounded, lossless encoding of already projected advisory history summaries."""
import base64
import binascii
import json
import zlib

# Mirrors api.ObsPostDeployBuildHistoryDecodedBytesMax. Encoded events still
# share the existing 16 KiB event-array budget; no source payload is admitted.
DECODED_MAX = 64 << 10
ENCODING = "zlib-base64-v1"
SECTIONS = ("loop_health", "builder_health", "handoff_health", "build_recovery_health", "app_task_health", "command_cron_health",
            "command_cron_outcomes", "http_cron_delivery", "delayed_task_dispatch", "command_cron_timing", "app_task_results", "app_task_operations", "post_capture_health", "snapshot_prime_health", "preparation_health")


def unique_object(pairs):
    out = {}
    for k, v in pairs:
        if k in out:
            raise ValueError("duplicate compact detail key")
        out[k] = v
    return out


def expand(event):
    from post_deployment_builds import require, integer, HISTORY_BYTES_MAX
    require(isinstance(event, dict))
    out = dict(event)
    if "omitted_sections" in event:
        omitted = event["omitted_sections"]
        require(isinstance(omitted, list) and omitted and all(isinstance(k, str) and k in SECTIONS for k in omitted))
        require(omitted == [k for k in SECTIONS if k in omitted] and not any(k in event for k in omitted))
        require(all(k in event for k in ("counts", "jobs", "evidence_generated_at")))
    if "compact_details" not in event:
        return out
    require("omitted_sections" not in event)
    p = event["compact_details"]
    require(isinstance(p, dict) and p.get("encoding") == ENCODING)
    size = integer(p.get("decoded_bytes"), DECODED_MAX)
    require(size > 0 and isinstance(p.get("data"), str) and 0 < len(p["data"]) <= HISTORY_BYTES_MAX)
    try:
        compressed = base64.b64decode(p["data"], validate=True)
        require(base64.b64encode(compressed).decode() == p["data"])
        decoder = zlib.decompressobj()
        body = decoder.decompress(compressed, size+1)
        require(len(body) == size and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail)
        fields = json.loads(body.decode("utf-8"), object_pairs_hook=unique_object)
    except (binascii.Error, zlib.error, UnicodeError) as e:
        raise ValueError("invalid compact build details") from e
    require(isinstance(fields, dict) and fields and all(k in SECTIONS and k not in event and isinstance(v, dict) for k, v in fields.items()))
    out.pop("compact_details")
    out.update(fields)
    return out


def encode(event):
    from post_deployment_builds import require
    out = dict(event)
    fields = {k: out.pop(k) for k in SECTIONS if k in out}
    require(fields)
    body = json.dumps(fields, allow_nan=False, separators=(",", ":"), sort_keys=True).encode()
    require(len(body) <= DECODED_MAX)
    out["compact_details"] = {"encoding": ENCODING, "decoded_bytes": len(body), "data": base64.b64encode(zlib.compress(body)).decode()}
    return out


def project_encoding(projected, original):
    # Never forward an opaque caller-supplied block. Encode only the closed
    # summaries which passed their existing independent validators.
    if "omitted_sections" in original:
        projected["omitted_sections"] = list(original["omitted_sections"])
    if "compact_details" in original and any(k in projected for k in SECTIONS):
        return encode(projected)
    return projected


def compact(event, maximum):
    from post_deployment_builds import build_history_bytes
    if build_history_bytes([event]) <= maximum:
        return event
    encoded = encode(event)
    if build_history_bytes([encoded]) <= maximum:
        return encoded
    out = {k: v for k, v in event.items() if k not in SECTIONS}
    out["omitted_sections"] = [k for k in SECTIONS if k in event]
    return out


def fit_projected_events(events, maximum):
    """Encoding implementations may change byte sizes after closed projection."""
    from post_deployment_builds import require, build_history_bytes
    out = [compact(expand(e), maximum) if build_history_bytes([e]) > maximum else e for e in events]
    omitted = 0
    while len(out) > 1 and build_history_bytes(out) > maximum:
        out.pop(0)
        omitted += 1
    require(build_history_bytes(out) <= maximum)
    return out, omitted
