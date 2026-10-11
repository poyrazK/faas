"""Thread-safe process-local MCP runtime catalog."""

from copy import deepcopy
import hashlib
import json
import re
import threading
from urllib.parse import urlsplit


NAME_PATTERN = re.compile(r"^[\x21-\x7e]{1,128}$")
VARIABLE_PATTERN = re.compile(r"\{([A-Za-z][A-Za-z0-9_.-]*)\}")


class RuntimeCatalog:
    def __init__(self):
        self._lock = threading.RLock()
        self._entries = {"tool": {}, "resource": {}, "prompt": {}}
        self._subscribers = set()
        self._revision = 0

    def seed(self, kind, definition):
        catalog_definition = json.loads(json.dumps(definition, allow_nan=False))
        name = catalog_definition.get("name")
        if not isinstance(name, str) or not NAME_PATTERN.fullmatch(name):
            raise ValueError("invalid built-in MCP catalog name")
        with self._lock:
            if name in self._entries[kind]:
                raise ValueError(f"duplicate built-in MCP {kind} {name!r}")
            self._entries[kind][name] = {"definition": catalog_definition, "handler": None}
            self._revision += 1

    def register(self, kind, definition, handler, replace_name=None, notify=True):
        if kind not in self._entries:
            raise ValueError("unknown MCP catalog kind")
        if not callable(handler):
            raise TypeError("MCP catalog handler must be callable")
        if not isinstance(definition, dict):
            raise TypeError("MCP catalog definition must be an object")
        try:
            catalog_definition = json.loads(json.dumps(definition, allow_nan=False))
        except (TypeError, ValueError):
            raise ValueError("MCP catalog definition must be JSON serializable") from None
        name = catalog_definition.get("name")
        if not isinstance(name, str) or not NAME_PATTERN.fullmatch(name):
            raise ValueError("MCP catalog name must be 1 to 128 visible ASCII characters without whitespace")
        if kind == "resource":
            uri = catalog_definition.get("uri")
            template = catalog_definition.get("uriTemplate")
            uri_present = "uri" in catalog_definition
            template_present = "uriTemplate" in catalog_definition
            if uri_present == template_present:
                raise ValueError("MCP resource must declare one valid uri or uriTemplate")
            if uri_present and (not isinstance(uri, str) or not valid_absolute_uri(uri)):
                raise ValueError("MCP resource uri must be an absolute URI")
            if template_present and (not isinstance(template, str) or not valid_uri_template(template)):
                raise ValueError("MCP resource uriTemplate must be a valid absolute URI template")

        key = replace_name or name
        with self._lock:
            entries = self._entries[kind]
            if replace_name is not None and key not in entries:
                raise ValueError(f"MCP {kind} {key!r} is not registered")
            if replace_name is None and name in entries:
                raise ValueError(f"MCP {kind} {name!r} is already registered")
            if replace_name is not None and name != key and name in entries:
                raise ValueError(f"MCP {kind} {name!r} is already registered")
            if kind == "resource":
                uri_key = catalog_definition.get("uri", catalog_definition.get("uriTemplate"))
                for other_name, existing in entries.items():
                    if other_name != key and existing["definition"].get("uri", existing["definition"].get("uriTemplate")) == uri_key:
                        raise ValueError(f"MCP resource URI {uri_key!r} is already registered")
            if replace_name is not None and name != key:
                del entries[key]
            entries[name] = {"definition": catalog_definition, "handler": handler}
            self._revision += 1
        if notify:
            self._publish()

    def remove(self, kind, name):
        with self._lock:
            if kind not in self._entries:
                return False
            removed = self._entries[kind].pop(name, None) is not None
            if removed:
                self._revision += 1
        if removed:
            self._publish()
        return removed

    def get(self, kind, name):
        with self._lock:
            entry = self._entries.get(kind, {}).get(name)
            if entry is None:
                return None
            return {"definition": deepcopy(entry["definition"]), "handler": entry["handler"]}

    def list(self, kind):
        with self._lock:
            entries = self._entries.get(kind, {})
            return [deepcopy(entries[name]["definition"]) for name in sorted(entries)]

    def contract_fingerprint(self, features=None):
        """Return the local revision and SHA-256 of advertised definitions."""
        with self._lock:
            revision = self._revision
            contract = {"features": features or {}}
            for kind in ("tool", "resource", "prompt"):
                contract[f"{kind}s"] = [
                    deepcopy(self._entries[kind][name]["definition"])
                    for name in sorted(self._entries[kind])
                ]
        canonical = json.dumps(
            contract, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False
        ).encode("utf-8")
        return revision, hashlib.sha256(canonical).hexdigest()

    def match_resource(self, uri):
        with self._lock:
            resources = [
                {"definition": deepcopy(entry["definition"]), "handler": entry["handler"]}
                for entry in self._entries["resource"].values()
            ]
        for entry in resources:
            definition = entry["definition"]
            if definition.get("uri") == uri:
                return {"definition": definition, "handler": entry["handler"], "variables": {}}
        for entry in resources:
            definition = entry["definition"]
            template = definition.get("uriTemplate")
            if isinstance(template, str):
                variables = match_uri_template(template, uri)
                if variables is not None:
                    return {"definition": definition, "handler": entry["handler"], "variables": variables}
        return None

    def subscribe(self, listener):
        if not callable(listener):
            raise TypeError("MCP catalog listener must be callable")
        with self._lock:
            self._subscribers.add(listener)
        active = True

        def unsubscribe():
            nonlocal active
            with self._lock:
                if active:
                    active = False
                    self._subscribers.discard(listener)

        return unsubscribe

    def _publish(self):
        with self._lock:
            listeners = tuple(self._subscribers)
        for listener in listeners:
            try:
                listener()
            except Exception:
                continue


def valid_absolute_uri(uri):
    if not isinstance(uri, str) or not uri:
        return False
    try:
        if len(uri.encode("utf-8")) > 16 * 1024:
            return False
    except UnicodeEncodeError:
        return False
    if any(ord(character) < 32 or ord(character) == 127 for character in uri):
        return False
    try:
        return bool(urlsplit(uri).scheme)
    except ValueError:
        return False


def valid_uri_template(template):
    if not VARIABLE_PATTERN.search(template):
        return False
    expanded = VARIABLE_PATTERN.sub("value", template)
    if "{" in expanded or "}" in expanded:
        return False
    return valid_absolute_uri(expanded)


def match_uri_template(template, uri):
    variables = list(VARIABLE_PATTERN.finditer(template))
    if not variables:
        return None
    expression = []
    position = 0
    for variable in variables:
        expression.append(re.escape(template[position:variable.start()]))
        expression.append(r"([^/?#]+)")
        position = variable.end()
    expression.append(re.escape(template[position:]))
    match = re.fullmatch("".join(expression), uri)
    if match is None:
        return None
    return {variable.group(1): value for variable, value in zip(variables, match.groups())}

