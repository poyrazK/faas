"""Validate modern HTTP request headers before invoking any MCP handler."""
import base64
import binascii
import math


def validate_mcp_headers(headers, message, protocol, catalog):
    params = message.get("params", {})
    params = params if isinstance(params, dict) else {}
    meta = params.get("_meta", {})
    meta = meta if isinstance(meta, dict) else {}
    version_key = "io.modelcontextprotocol/protocolVersion"
    if version_key in meta and meta[version_key] != protocol:
        return False
    if "id" not in message or protocol != "2026-07-28" or (not headers.get("MCP-Protocol-Version") and version_key not in meta):
        return True
    if headers.get_all("MCP-Method", []) != [message["method"]]:
        return False
    key = {"tools/call": "name", "prompts/get": "name", "resources/read": "uri"}.get(message["method"])
    if key and (not isinstance(params.get(key), str) or not params[key] or len(headers.get_all("MCP-Name", [])) != 1 or _decode_header(headers.get("MCP-Name", "")) != params[key]):
        return False
    if message["method"] == "tools/call":
        entry = catalog.get("tool", params.get("name"))
        if entry and not _validate_params(headers, entry["definition"].get("inputSchema", {}), params.get("arguments", {})):
            return False
    return True


def _validate_params(headers, schema, arguments):
    arguments = arguments if isinstance(arguments, dict) else {}
    for name, prop in schema.get("properties", {}).items():
        value = arguments.get(name)
        suffix = prop.get("x-mcp-header")
        if isinstance(suffix, str) and suffix:
            values = headers.get_all("MCP-Param-" + suffix, [])
            if value is None:
                if values:
                    return False
            elif len(values) != 1 or not _primitive_equal(values[0], value):
                return False
        if not _validate_params(headers, prop, value):
            return False
    return True


def _decode_header(header):
    if header.startswith("=?base64?"):
        if not header.endswith("?="):
            return None
        try:
            return base64.b64decode(header[9:-2], validate=True).decode("utf-8")
        except (binascii.Error, UnicodeError, ValueError):
            return None
    return header


def _primitive_equal(header, value):
    header = _decode_header(header)
    if header is None:
        return False
    if isinstance(value, str):
        return header == value
    if isinstance(value, bool):
        return header == str(value).lower()
    if isinstance(value, (int, float)):
        try:
            number = float(header)
            return math.isfinite(number) and number == value
        except ValueError:
            return False
    return False
