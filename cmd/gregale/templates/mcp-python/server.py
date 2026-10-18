"""Minimal stateless MCP Streamable HTTP server for Gregale."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import math
import os
import queue
import re
import select
import signal
import socket
import threading
import time
from urllib.parse import urlsplit

from protocol_telemetry import ProtocolObservation, start_protocol_heartbeat

from auth import AuthConfigurationError, AuthFailure, Authenticator
from catalog import RuntimeCatalog
from tasks import DurableTaskRuntime, TASK_ID_PATTERN, TaskNotFound, register_task_handler, resolve_task_role, task_id_from_uri, task_resource_template

CURRENT_PROTOCOL = "2026-07-28"
LEGACY_PROTOCOL = "2025-11-25"
SUPPORTED_PROTOCOLS = {CURRENT_PROTOCOL, LEGACY_PROTOCOL}
MAX_REQUEST_BYTES = 1 << 20
MAX_RESOURCE_SUBSCRIPTIONS = 64
MAX_RESOURCE_URI_BYTES = 16 * 1024
MAX_SUBSCRIPTION_STREAMS = 128
PROTOCOL_VERSION_META_KEY = "io.modelcontextprotocol/protocolVersion"
CLIENT_CAPABILITIES_META_KEY = "io.modelcontextprotocol/clientCapabilities"
SUBSCRIPTION_ID_META_KEY = "io.modelcontextprotocol/subscriptionId"
SERVER_INFO_META_KEY = "io.modelcontextprotocol/serverInfo"
TASKS_EXTENSION_ID = "io.modelcontextprotocol/tasks"
MAX_TASK_SUBSCRIPTION_IDS = 64
TASK_SUBSCRIPTION_KEEPALIVE_SECONDS = 10
SUBSCRIPTION_STREAM_SLOTS = threading.BoundedSemaphore(MAX_SUBSCRIPTION_STREAMS)


class ResourceUpdateHub:
    """Process-local resource event adapter; replace it with shared pub/sub for replicas."""

    def __init__(self):
        self._lock = threading.RLock()
        self._subscribers = {}

    def subscribe(self, uri, listener):
        if not valid_resource_update_uri(uri) or not callable(listener):
            return lambda: None
        with self._lock:
            listeners = self._subscribers.setdefault(uri, set())
            listeners.add(listener)

        active = True

        def unsubscribe():
            nonlocal active
            with self._lock:
                if not active:
                    return
                active = False
                listeners = self._subscribers.get(uri)
                if listeners is None:
                    return
                listeners.discard(listener)
                if not listeners:
                    self._subscribers.pop(uri, None)

        return unsubscribe

    def publish(self, uri):
        if not valid_resource_update_uri(uri):
            return
        with self._lock:
            listeners = tuple(self._subscribers.get(uri, ()))
        for listener in listeners:
            try:
                listener(uri)
            except Exception:
                # A broken listener must not block other active subscriptions.
                continue


def valid_resource_update_uri(uri):
    if not isinstance(uri, str) or not uri:
        return False
    try:
        if len(uri.encode("utf-8")) > MAX_RESOURCE_URI_BYTES:
            return False
    except UnicodeError:
        return False
    if any(ord(character) < 32 or ord(character) == 127 for character in uri):
        return False
    try:
        return bool(urlsplit(uri).scheme)
    except ValueError:
        return False


RESOURCE_UPDATE_ADAPTER = ResourceUpdateHub()


def set_resource_update_adapter(adapter):
    """Install an application-owned shared adapter before starting the HTTP server."""
    if not callable(getattr(adapter, "subscribe", None)) or not callable(getattr(adapter, "publish", None)):
        raise TypeError("resource update adapter must implement subscribe(uri, listener) and publish(uri)")
    global RESOURCE_UPDATE_ADAPTER
    RESOURCE_UPDATE_ADAPTER = adapter


def notify_resource_updated(uri):
    """Publish a resource change hint; clients fetch the current data with resources/read."""
    if not valid_resource_update_uri(uri):
        raise ValueError("resource update URI must be an absolute URI under 16 KiB")
    RESOURCE_UPDATE_ADAPTER.publish(uri)
SUMMARY_STYLES = ("brief", "technical", "executive")
SAMPLE_RECORD_IDS = ("example-1", "example-2", "example-3")
SAMPLE_RECORD_OWNERS = {"example-1":"demo-caller-a","example-2":"demo-caller-b","example-3":"demo-caller-a"}

TOOLS = [
    {
        "name": "greet",
        "description": "Return a greeting.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {"name": {"type": "string", "minLength": 1, "maxLength": 100}},
            "required": ["name"],
        },
        "annotations": {
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
    },
    {
        "name": "stream_demo",
        "description": "Send three progress events over about one second.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {},
        },
        "annotations": {
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
    },
    {
        "name": "add",
        "description": "Add two finite numbers.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {"a": {"type": "number"}, "b": {"type": "number"}},
            "required": ["a", "b"],
        },
        "outputSchema": {
            "type": "object",
            "properties": {"sum": {"type": "number"}},
            "required": ["sum"],
        },
        "annotations": {
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
    },
    {
        "name": "build_report",
        "description": "Build a harmless sample report; supports durable MCP Tasks when configured.",
        "inputSchema": {
            "type": "object",
            "additionalProperties": False,
            "properties": {
                "report": {"type": "string", "minLength": 1, "maxLength": 80},
                "steps": {"type": "integer", "minimum": 1, "maximum": 20},
                "request_approval": {"type": "boolean", "description": "Pause the durable task for client approval before generating the report."},
            },
            "required": ["report", "steps"],
        },
        "annotations": {
            "readOnlyHint": True,
            "destructiveHint": False,
            "idempotentHint": True,
            "openWorldHint": False,
        },
    },
]

RESOURCES = [
    {
        "uri": "greeting://welcome",
        "name": "welcome",
        "title": "Welcome",
        "description": "A public example resource.",
        "mimeType": "text/plain",
    },
]

RESOURCE_TEMPLATES = [
    {
        "uriTemplate": "customer://records/{recordId}",
        "name": "customer_record",
        "title": "Customer record",
        "description": "Example customer-record resource; enforce ownership in your own data lookup.",
        "mimeType": "application/json",
    },
]

PROMPTS = [
    {
        "name": "summarize",
        "title": "Summarize text",
        "description": "Create a concise summary request.",
        "arguments": [
            {"name": "text", "description": "Text to summarize.", "required": True},
            {"name": "style", "description": "Optional summary style: brief, technical, or executive.", "required": False},
        ],
    },
]

MCP_CATALOG = RuntimeCatalog()
for _definition in TOOLS:
    MCP_CATALOG.seed("tool", _definition)
for _definition in RESOURCES + RESOURCE_TEMPLATES:
    MCP_CATALOG.seed("resource", _definition)
for _definition in PROMPTS:
    MCP_CATALOG.seed("prompt", _definition)


def register_tool(definition, handler):
    """Add a process-local tool; OAuth allow-lists must include its name."""
    MCP_CATALOG.register("tool", definition, handler)


def replace_tool(name, definition, handler):
    MCP_CATALOG.register("tool", definition, handler, replace_name=name)


def remove_tool(name):
    return MCP_CATALOG.remove("tool", name)


def register_resource(definition, handler):
    """Add a URI or URI-template resource with a process-local read handler."""
    MCP_CATALOG.register("resource", definition, handler)


def replace_resource(name, definition, handler):
    MCP_CATALOG.register("resource", definition, handler, replace_name=name)


def remove_resource(name):
    return MCP_CATALOG.remove("resource", name)


def register_prompt(definition, handler):
    MCP_CATALOG.register("prompt", definition, handler)


def replace_prompt(name, definition, handler):
    MCP_CATALOG.register("prompt", definition, handler, replace_name=name)


def remove_prompt(name):
    return MCP_CATALOG.remove("prompt", name)


def runtime_catalog_health(expected_sha256, tasks_enabled=False, auth_config=None):
    auth_config = auth_config or {}
    auth_policy = {
        field: auth_config.get(field)
        for field in ("tool_scopes", "resource_scopes", "prompt_scopes")
    }
    revision, digest = MCP_CATALOG.contract_fingerprint({
        "tasks_enabled": tasks_enabled,
        "auth_policy": auth_policy,
    })
    ready = not expected_sha256 or digest == expected_sha256
    return {
        "ready": ready,
        "status": "ok" if ready else "degraded",
        "catalog_revision": revision,
        "catalog_sha256": digest,
    }


def request_context(granted_scopes):
    return {
        "subject": getattr(granted_scopes, "subject", ""),
        "client_id": getattr(granted_scopes, "client_id", ""),
        "scopes": sorted(granted_scopes),
    }


def rpc_response(request_id, result):
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def protocol_response(value, protocol):
    if protocol != CURRENT_PROTOCOL or not isinstance(value.get("result"), dict):
        return value
    result = dict(value["result"])
    result.setdefault("resultType", "complete")
    if any(key in result for key in ("tools", "resources", "resourceTemplates", "prompts", "contents", "supportedVersions")):
        result.update(ttlMs=0, cacheScope="private")
    return {**value, "result": result}


def rpc_error(request_id, code, message):
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}}


def tool_error(request_id, message):
    return rpc_response(request_id, {
        "isError": True,
        "content": [{"type": "text", "text": message}],
    })


def finite_number(value):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return False
    try:
        return math.isfinite(value)
    except OverflowError:
        return False


def reject_json_constant(_value):
    raise ValueError("non-finite JSON number")


def serializable_json(value):
    try:
        json.dumps(value, allow_nan=False)
        return True
    except (TypeError, ValueError, UnicodeError):
        return False


def valid_task_subscription_id(value):
    if isinstance(value, str):
        return bool(value)
    if isinstance(value, bool):
        return False
    if isinstance(value, int):
        return abs(value) <= 9_007_199_254_740_991
    return finite_number(value) and value.is_integer() and abs(value) <= 9_007_199_254_740_991


def accepts_event_stream(header):
    for entry in header.split(","):
        media_type, *parameters = entry.strip().split(";")
        if media_type.strip().lower() != "text/event-stream":
            continue
        accepted = True
        for parameter in parameters:
            name, separator, value = parameter.strip().partition("=")
            if separator and name.strip().lower() == "q":
                try:
                    quality = float(value.strip().strip('"'))
                except ValueError:
                    accepted = False
                    break
                if not math.isfinite(quality) or quality <= 0 or quality > 1:
                    accepted = False
                    break
        if accepted:
            return True
    return False


def dispatch(message, protocol, authenticator, granted_scopes, task_runtime=None):
    request_id = message.get("id")
    method = message.get("method")
    params = message.get("params", {})
    if not isinstance(params, dict):
        return 400, rpc_error(request_id, -32602, "params must be an object")

    if method == "server/discover":
        if protocol != CURRENT_PROTOCOL:
            return 404, rpc_error(request_id, -32601, "method not found")
        return 200, rpc_response(request_id, {
            "supportedVersions": [CURRENT_PROTOCOL, LEGACY_PROTOCOL],
            "capabilities": server_capabilities(task_runtime, modern=True),
        })
    if method == "initialize":
        requested = params.get("protocolVersion")
        if requested not in SUPPORTED_PROTOCOLS:
            return 400, rpc_error(request_id, -32602, "unsupported protocol version")
        return 200, rpc_response(request_id, {
            "protocolVersion": requested,
            "capabilities": server_capabilities(task_runtime, modern=requested == CURRENT_PROTOCOL),
            "serverInfo": {"name": "gregale-mcp-python", "version": "1.0.0"},
        })
    if method == "ping":
        return 200, rpc_response(request_id, {})
    if method == "tools/list":
        return 200, rpc_response(request_id, {"tools": visible_tools(authenticator, granted_scopes)})
    if method == "tools/call":
        return 200, call_tool(request_id, params, authenticator, granted_scopes, protocol, task_runtime)
    if method == "resources/list":
        return 200, rpc_response(request_id, {"resources": visible_resources(authenticator, granted_scopes)})
    if method == "resources/templates/list":
        return 200, rpc_response(request_id, {"resourceTemplates": visible_resource_templates(authenticator, granted_scopes, task_runtime)})
    if method == "resources/read":
        return read_resource(request_id, params, authenticator, granted_scopes, task_runtime)
    if method == "prompts/list":
        return 200, rpc_response(request_id, {"prompts": visible_prompts(authenticator, granted_scopes)})
    if method == "prompts/get":
        return get_prompt(request_id, params, authenticator, granted_scopes)
    if method == "completion/complete":
        return complete(request_id, params, authenticator, granted_scopes)
    if method == "tasks/get":
        if task_runtime is None:
            return 404, rpc_error(request_id, -32601, "method not found")
        try:
            task, _record = task_runtime.get(params.get("taskId"), granted_scopes, authenticator, granted_scopes)
            return 200, rpc_response(request_id, task)
        except TaskNotFound:
            return 404, rpc_error(request_id, -32002, "task not found")
    if method == "tasks/cancel":
        if task_runtime is None:
            return 404, rpc_error(request_id, -32601, "method not found")
        try:
            task_runtime.cancel(params.get("taskId"), granted_scopes, authenticator, granted_scopes)
            return 200, rpc_response(request_id, {"resultType": "complete"})
        except TaskNotFound:
            return 404, rpc_error(request_id, -32002, "task not found")
    if method == "tasks/update":
        if task_runtime is None:
            return 404, rpc_error(request_id, -32601, "method not found")
        input_responses = params.get("inputResponses")
        if not isinstance(input_responses, dict):
            return 400, rpc_error(request_id, -32602, "Task input responses must be an object")
        try:
            task_runtime.update_inputs(params.get("taskId"), input_responses, granted_scopes, authenticator, granted_scopes)
            return 200, rpc_response(request_id, {"resultType": "complete"})
        except TaskNotFound:
            return 404, rpc_error(request_id, -32002, "task not found")
        except ValueError as exc:
            return 400, rpc_error(request_id, -32602, str(exc))
        except Exception:
            return 500, rpc_error(request_id, -32603, "could not update MCP task")
    return 404, rpc_error(request_id, -32601, "method not found")


def server_capabilities(task_runtime, modern):
    resources = {"listChanged": True}
    if modern:
        resources["subscribe"] = True
    capabilities = {
        "tools": {"listChanged": True},
        "resources": resources,
        "prompts": {"listChanged": True},
        "completions": {},
    }
    if task_runtime and modern:
        capabilities["extensions"] = {"io.modelcontextprotocol/tasks": {}}
    return capabilities


def visible_tools(authenticator, granted_scopes):
    return [tool for tool in MCP_CATALOG.list("tool") if authenticator.can_access_tool(tool["name"], granted_scopes)]


def visible_resources(authenticator, granted_scopes):
    return [resource for resource in MCP_CATALOG.list("resource") if "uri" in resource and authenticator.can_access_resource(resource["uri"], granted_scopes)]


def visible_resource_templates(authenticator, granted_scopes, task_runtime):
    visible = [resource for resource in MCP_CATALOG.list("resource") if "uriTemplate" in resource and authenticator.can_access_resource(resource["uriTemplate"], granted_scopes)]
    if task_runtime and authenticator.can_access_resource("task://tasks/{taskId}", granted_scopes):
        visible.append(task_resource_template())
    return visible


def configured_resource_uri(uri):
    return MCP_CATALOG.match_resource(uri) is not None


def visible_prompts(authenticator, granted_scopes):
    return [prompt for prompt in MCP_CATALOG.list("prompt") if authenticator.can_access_prompt(prompt["name"], granted_scopes)]


def subscription_catalog_fingerprint(key, authenticator, granted_scopes, task_runtime):
    if key == "toolsListChanged":
        value = visible_tools(authenticator, granted_scopes)
    elif key == "resourcesListChanged":
        value = {
            "resources": visible_resources(authenticator, granted_scopes),
            "resourceTemplates": visible_resource_templates(authenticator, granted_scopes, task_runtime),
        }
    elif key == "promptsListChanged":
        value = visible_prompts(authenticator, granted_scopes)
    else:
        raise ValueError("unknown catalog notification filter")
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)


def complete(request_id, params, authenticator, granted_scopes):
    reference = params.get("ref")
    argument = params.get("argument")
    if not isinstance(reference, dict) or not isinstance(argument, dict):
        return 400, rpc_error(request_id, -32602, "completion reference and argument are required")
    argument_name = argument.get("name")
    value = argument.get("value")
    if not isinstance(argument_name, str) or not argument_name or not isinstance(value, str):
        return 400, rpc_error(request_id, -32602, "completion argument name and value must be strings")

    reference_type = reference.get("type")
    if reference_type == "ref/prompt":
        prompt_name = reference.get("name")
        if (prompt_name != "summarize" or argument_name != "style"
                or MCP_CATALOG.get("prompt", prompt_name) is None
                or not authenticator.can_access_prompt(prompt_name, granted_scopes)):
            return 400, rpc_error(request_id, -32602, "unknown completion reference or argument")
        candidates = SUMMARY_STYLES
    elif reference_type == "ref/resource":
        resource_uri = reference.get("uri")
        registered = any(
            resource.get("uriTemplate") == resource_uri
            for resource in MCP_CATALOG.list("resource")
        )
        if (resource_uri != "customer://records/{recordId}" or argument_name != "recordId"
                or not registered or not authenticator.can_access_resource(resource_uri, granted_scopes)):
            return 400, rpc_error(request_id, -32602, "unknown completion reference or argument")
        candidates = SAMPLE_RECORD_IDS
    else:
        return 400, rpc_error(request_id, -32602, "unknown completion reference")

    prefix = value.lower()
    values = [candidate for candidate in candidates if candidate.lower().startswith(prefix)]
    return 200, rpc_response(request_id, {
        "completion": {"values": values, "total": len(values), "hasMore": False},
    })


def call_tool(request_id, params, authenticator, granted_scopes, protocol=CURRENT_PROTOCOL, task_runtime=None):
    name = params.get("name")
    arguments = params.get("arguments", {})
    if not isinstance(name, str) or not name:
        return rpc_error(request_id, -32602, "tool name is required")
    if not isinstance(arguments, dict):
        return rpc_error(request_id, -32602, "arguments must be an object")
    if isinstance(name, str) and not authenticator.can_access_tool(name, granted_scopes):
        return tool_error(request_id, "tool access denied")
    entry = MCP_CATALOG.get("tool", name)
    if entry is None:
        return tool_error(request_id, "unknown tool")
    metadata = params.get("_meta", {})
    client_capabilities = metadata.get(CLIENT_CAPABILITIES_META_KEY, {}) if isinstance(metadata, dict) else {}
    extensions = client_capabilities.get("extensions", {}) if isinstance(client_capabilities, dict) else {}
    task_capability = extensions.get(TASKS_EXTENSION_ID) if isinstance(extensions, dict) else None
    if (task_runtime and protocol == CURRENT_PROTOCOL and task_capability is not None and task_capability is not False
            and task_runtime.can_create(name)):
        if name == "build_report" and arguments.get("request_approval") is True:
            elicitation = client_capabilities.get("elicitation") if isinstance(client_capabilities, dict) else None
            form_capable = isinstance(elicitation, dict) and (bool(elicitation.get("form")) or not elicitation)
            if not form_capable:
                return tool_error(request_id, "request_approval requires client support for elicitation form requests")
        try:
            return rpc_response(request_id, task_runtime.create(name, arguments, granted_scopes, client_capabilities))
        except ValueError:
            return tool_error(request_id, "invalid task arguments or task queue capacity reached")
        except Exception:
            return rpc_error(request_id, -32603, "could not create MCP task")
    if entry["handler"] is not None:
        try:
            result = entry["handler"](arguments, request_context(granted_scopes))
        except Exception:
            return tool_error(request_id, "tool execution failed")
        if (not isinstance(result, dict) or not isinstance(result.get("content"), list)
                or not serializable_json(result)):
            return tool_error(request_id, "tool handler returned an invalid result")
        return rpc_response(request_id, result)
    if name == "greet":
        value = arguments.get("name")
        if set(arguments) != {"name"} or not isinstance(value, str) or not value.strip() or len(value) > 100:
            return tool_error(request_id, "name must be a non-empty string of at most 100 bytes")
        return rpc_response(request_id, {"content": [{"type": "text", "text": f"Hello, {value}!"}]})
    if name == "add":
        a = arguments.get("a")
        b = arguments.get("b")
        if set(arguments) != {"a", "b"} or not finite_number(a) or not finite_number(b):
            return tool_error(request_id, "a and b must be finite numbers")
        total = a + b
        if not finite_number(total):
            return tool_error(request_id, "sum exceeds the finite number range")
        return rpc_response(request_id, {
            "content": [{"type": "text", "text": str(total)}],
            "structuredContent": {"sum": total},
        })
    if name == "stream_demo":
        if arguments:
            return tool_error(request_id, "stream_demo does not accept arguments")
        return rpc_response(request_id, {"content": [{"type": "text", "text": "Streaming complete."}]})
    if name == "build_report":
        report = arguments.get("report")
        steps = arguments.get("steps")
        require_approval = arguments.get("request_approval", False)
        valid_steps = (finite_number(steps) and int(steps) == steps and 1 <= steps <= 20)
        if (set(arguments) not in ({"report", "steps"}, {"report", "steps", "request_approval"})
                or "request_approval" in arguments and not isinstance(require_approval, bool)
                or not isinstance(report, str) or not report.strip()
                or len(report.encode("utf-16-le", errors="surrogatepass")) // 2 > 80 or not valid_steps):
            return tool_error(request_id, "report must be 1 to 80 characters and steps must be an integer from 1 to 20")
        steps = int(steps)
        arguments["steps"] = steps
        if require_approval:
            return tool_error(request_id, "request_approval requires durable MCP Tasks and client elicitation support")
        time.sleep(steps * 0.2)
        return rpc_response(request_id, {"content": [{"type": "text", "text": f"Report {report} is ready after {steps} steps."}]})
    return tool_error(request_id, "unknown tool")


def read_resource(request_id, params, authenticator, granted_scopes, task_runtime=None):
    uri = params.get("uri")
    if not isinstance(uri, str) or not uri:
        return 400, rpc_error(request_id, -32602, "resource URI is required")
    if not authenticator.can_access_resource(uri, granted_scopes):
        failure = authenticator.resource_failure(uri, granted_scopes)
        return failure.status, {"error": failure.code}
    task_id = task_id_from_uri(uri)
    if task_id and task_runtime:
        try:
            task, _record = task_runtime.get(task_id, granted_scopes, authenticator, granted_scopes)
        except TaskNotFound:
            return 404, rpc_error(request_id, -32002, "resource not found")
        content = {"uri": uri, "mimeType": "application/json", "text": json.dumps(task, separators=(",", ":"), allow_nan=False)}
        return 200, rpc_response(request_id, {"contents": [content]})
    entry = MCP_CATALOG.match_resource(uri)
    if entry is None:
        return 404, rpc_error(request_id, -32002, "resource not found")
    if entry["handler"] is not None:
        try:
            contents = entry["handler"](uri, entry["variables"], request_context(granted_scopes))
        except Exception:
            return 500, rpc_error(request_id, -32603, "resource read failed")
        if (not isinstance(contents, list) or any(not isinstance(content, dict) for content in contents)
                or not serializable_json(contents)):
            return 500, rpc_error(request_id, -32603, "resource handler returned an invalid result")
        return 200, rpc_response(request_id, {"contents": contents})
    if uri == "greeting://welcome":
        content = {"uri": uri, "mimeType": "text/plain", "text": "Welcome to the Gregale MCP starter."}
        return 200, rpc_response(request_id, {"contents": [content]})
    prefix = "customer://records/"
    record_id = uri[len(prefix):] if uri.startswith(prefix) else ""
    if record_id not in SAMPLE_RECORD_OWNERS or (authenticator.mode!="open" and getattr(granted_scopes,"subject","")!=SAMPLE_RECORD_OWNERS.get(record_id)):
        return 404, rpc_error(request_id, -32002, "resource not found")
    content = {
        "uri": uri,
        "mimeType": "application/json",
        "text": json.dumps({"recordId": record_id, "status": "example"}, separators=(",", ":")),
    }
    return 200, rpc_response(request_id, {"contents": [content]})


def get_prompt(request_id, params, authenticator, granted_scopes):
    name = params.get("name")
    if not isinstance(name, str) or not name:
        return 400, rpc_error(request_id, -32602, "prompt name is required")
    if not authenticator.can_access_prompt(name, granted_scopes):
        failure = authenticator.prompt_failure(name, granted_scopes)
        return failure.status, {"error": failure.code}
    entry = MCP_CATALOG.get("prompt", name)
    if entry is None:
        return 400, rpc_error(request_id, -32602, "prompt not found")
    if entry["handler"] is not None:
        arguments = params.get("arguments", {})
        if arguments is None:
            arguments = {}
        if not isinstance(arguments, dict):
            return 400, rpc_error(request_id, -32602, "prompt arguments must be an object")
        try:
            result = entry["handler"](arguments, request_context(granted_scopes))
        except Exception:
            return 500, rpc_error(request_id, -32603, "prompt execution failed")
        if (not isinstance(result, dict) or not isinstance(result.get("messages"), list)
                or not serializable_json(result)):
            return 500, rpc_error(request_id, -32603, "prompt handler returned an invalid result")
        return 200, rpc_response(request_id, result)
    if name != "summarize":
        return 400, rpc_error(request_id, -32602, "prompt not found")
    arguments = params.get("arguments", {})
    if arguments is None:
        arguments = {}
    if not isinstance(arguments, dict):
        return 400, rpc_error(request_id, -32602, "prompt arguments must be an object")
    text = arguments.get("text")
    style = arguments.get("style")
    if (
        not isinstance(text, str)
        or not text
        or len(text) > 4000
        or len(arguments) > 2
        or "style" in arguments and (not isinstance(style, str) or style not in SUMMARY_STYLES)
    ):
        return 400, rpc_error(request_id, -32602, "text must be 1 to 4000 characters and style must be brief, technical, or executive")
    if style:
        description = f"Ask for a {style} summary."
        instruction = f"Summarize this text in a {style} style:"
    else:
        description = "Ask for a concise summary."
        instruction = "Summarize this text concisely:"
    return 200, rpc_response(request_id, {
        "description": description,
        "messages": [{"role": "user", "content": {"type": "text", "text": f"{instruction}\n\n{text}"}}],
    })


class DrainingHTTPServer(ThreadingHTTPServer):
    def __init__(self, *args, **kwargs):
        self.draining = threading.Event()
        self._connections = set()
        self._connections_lock = threading.Condition()
        super().__init__(*args, **kwargs)

    def process_request(self, request, client_address):
        with self._connections_lock:
            self._connections.add(request)
        try:
            super().process_request(request, client_address)
        except Exception:
            with self._connections_lock:
                self._connections.discard(request)
                self._connections_lock.notify_all()
            raise

    def shutdown_request(self, request):
        try:
            super().shutdown_request(request)
        finally:
            with self._connections_lock:
                self._connections.discard(request)
                self._connections_lock.notify_all()

    def drain_connections(self, timeout):
        deadline = time.monotonic() + timeout
        with self._connections_lock:
            while self._connections and time.monotonic() < deadline:
                self._connections_lock.wait(timeout=max(0, deadline - time.monotonic()))
            pending = list(self._connections)
        for connection in pending:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            connection.close()


class MCPHandler(BaseHTTPRequestHandler):
    server_version = "GregaleMCPPython/1.0"
    sys_version = ""
    protocol_version = "HTTP/1.1"

    def send_header(self, name, value):
        observation = getattr(self, "_protocol_observation", None)
        if observation and name.lower() == "content-type" and value.startswith("text/event-stream"):
            observation.stream_start = time.monotonic()
        super().send_header(name, value)

    def end_headers(self):
        headers = getattr(self, "headers", None)
        origins = headers.get_all("Origin", []) if headers is not None else []
        if len(origins) == 1 and origins[0] in self.server.app_config.get("allowed_origins", []):
            self.send_header("Access-Control-Allow-Origin", origins[0])
            self.send_header("Access-Control-Expose-Headers", "WWW-Authenticate, MCP-Protocol-Version, Streaming-Status")
        self.send_header("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
        super().end_headers()

    def do_OPTIONS(self):
        if self.server.draining.is_set():
            self.send_json(503, {"error": "mcp_server_draining"})
            return
        if self.reject_origin():
            return
        if urlsplit(self.path).path != self.server.app_config["endpoint"]:
            self.send_text(404, "not found")
            return
        if not self.headers.get_all("Origin", []):
            self.send_json(405, {"error": "origin_required"}, {"Allow": "POST, OPTIONS"})
            return
        methods = self.headers.get_all("Access-Control-Request-Method", [])
        if len(methods) != 1 or methods[0] not in {"GET", "POST"}:
            self.send_json(400, {"error": "method_not_allowed"})
            return
        headers = [h.strip().lower() for value in self.headers.get_all("Access-Control-Request-Headers", []) for h in value.split(",") if h.strip()]
        standard = {"authorization", "content-type", "accept", "mcp-protocol-version", "mcp-method", "mcp-name"}
        if any(h not in standard and not re.fullmatch(r"mcp-param-[!#$%&'*+.^_`|~0-9a-z-]+", h) for h in headers):
            self.send_json(400, {"error": "header_not_allowed"})
            return
        self.send_response(204)
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", ", ".join(headers))
        self.send_header("Content-Length", "0")
        self.end_headers()

    def setup(self):
        super().setup()
        self.connection.settimeout(15)

    def do_GET(self):
        if self.server.draining.is_set():
            self.send_json(503, {"error": "mcp_server_draining"})
            return
        if self.reject_origin():
            return
        path = urlsplit(self.path).path
        if path == "/healthz":
            health = runtime_catalog_health(
                getattr(self.server, "expected_catalog_sha256", ""),
                getattr(self.server, "task_runtime", None) is not None,
                self.server.app_config.get("auth", {}),
            )
            health["ok"] = health["ready"]
            health["instance_id"] = os.environ.get("FAAS_INSTANCE_ID", "").strip()
            self.send_json(200 if health["ready"] else 503, {key: value for key, value in health.items() if key != "ready"})
            return
        if path == self.server.app_config["endpoint"]:
            try:
                self.server.authenticator.authenticate(self.headers.get("Authorization", ""))
            except AuthFailure as failure:
                self.send_auth_failure(failure)
                return
            self.send_response(405)
            self.send_header("Allow", "POST")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if path == self.server.authenticator.metadata_path:
            metadata = self.server.authenticator.metadata()
            if metadata is not None:
                self.send_json(200, metadata)
                return
        self.send_response(404)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_POST(self):
        self._protocol_observation = None
        try:
            self._handle_post()
        except TimeoutError:
            if self._protocol_observation:self._protocol_observation.failed("timeout")
            raise
        except OSError:
            if self._protocol_observation:self._protocol_observation.failed("cancelled")
            raise
        except Exception:
            if self._protocol_observation:self._protocol_observation.failed("internal_error")
            raise
        finally:
            if self._protocol_observation:self._protocol_observation.finish()

    def _handle_post(self):
        if self.server.draining.is_set():
            self.send_json(503, {"error": "mcp_server_draining"})
            return
        if self.reject_origin():
            return
        if urlsplit(self.path).path != self.server.app_config["endpoint"]:
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        health = runtime_catalog_health(
            getattr(self.server, "expected_catalog_sha256", ""),
            getattr(self.server, "task_runtime", None) is not None,
            self.server.app_config.get("auth", {}),
        )
        if not health["ready"]:
            self.send_json(503, {"error": "catalog_contract_mismatch"}, {"Retry-After": "5"})
            return
        try:
            granted_scopes = self.server.authenticator.authenticate(self.headers.get("Authorization", ""))
        except AuthFailure as failure:
            self.send_auth_failure(failure)
            return
        content_type = self.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
        if content_type != "application/json":
            self.send_text(415, "content type must be application/json")
            return
        try:
            length = int(self.headers.get("Content-Length", ""))
        except ValueError:
            self.send_text(400, "invalid content length")
            return
        if length <= 0 or length > MAX_REQUEST_BYTES:
            self.send_text(413, "request body is missing or exceeds 1 MiB")
            return
        try:
            message = json.loads(
                self.rfile.read(length),
                parse_constant=reject_json_constant,
            )
        except (json.JSONDecodeError, UnicodeDecodeError, ValueError, RecursionError):
            self.send_rpc(400, rpc_error(None, -32700, "parse error"))
            return
        if not isinstance(message, dict) or message.get("jsonrpc") != "2.0" or not isinstance(message.get("method"), str) or not message["method"]:
            self.send_rpc(400, rpc_error(None, -32600, "invalid JSON-RPC request"))
            return
        failure = self.server.authenticator.authorize_request(
            message["method"], message.get("params", {}), granted_scopes
        )
        if failure is not None:
            self.send_auth_failure(failure)
            return
        if "id" in message:
            self._protocol_observation = ProtocolObservation(message, self.headers, getattr(self.server, "protocol_telemetry_sink", None))
        if "id" in message and (
            isinstance(message["id"], bool)
            or not isinstance(message["id"], (str, int, float))
            or isinstance(message["id"], float) and not math.isfinite(message["id"])
        ):
            self.send_rpc(400, rpc_error(None, -32600, "invalid JSON-RPC request ID"))
            return
        if "id" not in message:
            # MCP notifications do not carry a response body or mutate state.
            if message["method"] == "subscriptions/listen":
                self.send_rpc(400, rpc_error(None, -32600, "subscriptions/listen requires a JSON-RPC request ID"))
                return
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        protocol = self.headers.get("MCP-Protocol-Version", CURRENT_PROTOCOL)
        if protocol not in SUPPORTED_PROTOCOLS:
            self.send_rpc(400, rpc_error(message.get("id"), -32600, "unsupported MCP protocol version"))
            return
        params = message.get("params", {})
        if message["method"] == "subscriptions/listen":
            self.serve_task_subscription(message["id"], params, protocol, granted_scopes)
            return
        stream_tool = MCP_CATALOG.get("tool", "stream_demo") if isinstance(params, dict) and params.get("name") == "stream_demo" else None
        if message["method"] == "tools/call" and stream_tool is not None and stream_tool["handler"] is None:
            if not self.server.authenticator.can_access_tool("stream_demo", granted_scopes):
                self.send_rpc(200, tool_error(message["id"], "tool access denied"), protocol)
                return
            self.serve_stream_demo(message["id"], params, protocol)
            return
        status, response = dispatch(message, protocol, self.server.authenticator, granted_scopes, self.server.task_runtime)
        self.send_rpc(status, response, protocol)

    def serve_stream_demo(self, request_id, params, protocol):
        arguments = params.get("arguments", {})
        metadata = params.get("_meta", {})
        if not isinstance(arguments, dict) or arguments:
            self.send_rpc(400, rpc_error(request_id, -32602, "stream_demo arguments must be an empty object"), protocol)
            return
        if metadata is None:
            metadata = {}
        if not isinstance(metadata, dict):
            self.send_rpc(400, rpc_error(request_id, -32602, "tool metadata must be an object"), protocol)
            return
        has_progress_token = "progressToken" in metadata
        progress_token = metadata.get("progressToken")
        if has_progress_token and not (isinstance(progress_token, str) or finite_number(progress_token)):
            self.send_rpc(400, rpc_error(request_id, -32602, "progressToken must be a string or finite number"), protocol)
            return
        use_sse = has_progress_token and accepts_event_stream(self.headers.get("Accept", ""))
        if use_sse:
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache, no-transform")
            self.send_header("Transfer-Encoding", "chunked")
            self.send_header("Connection", "keep-alive")
            self.send_header("X-Accel-Buffering", "no")
            self.send_header("MCP-Protocol-Version", protocol)
            self.end_headers()
            self.wfile.flush()

        for step in range(1, 4):
            if self.client_disconnected():
                return
            if use_sse:
                notification = {
                    "jsonrpc": "2.0",
                    "method": "notifications/progress",
                    "params": {"progressToken": progress_token, "progress": step, "total": 3},
                }
                try:
                    self.write_sse_message(notification)
                except OSError:
                    self.mark_protocol_failure("cancelled")
                    return
            if not self.wait_for_client(0.4):
                return

        result = rpc_response(request_id, {
            "content": [{"type": "text", "text": "Streaming complete."}],
        })
        if use_sse:
            try:
                self.write_sse_message(protocol_response(result, protocol))
                self.wfile.write(b"0\r\n\r\n")
                self.wfile.flush()
            except OSError:
                self.mark_protocol_failure("cancelled")
                return
        else:
            self.send_rpc(200, result, protocol)

    def serve_task_subscription(self, request_id, params, protocol, granted_scopes):
        if not valid_task_subscription_id(request_id):
            self.send_rpc(400, rpc_error(None, -32600, "invalid JSON-RPC request ID"), protocol)
            return
        if (
            protocol != CURRENT_PROTOCOL
            or self.headers.get("MCP-Protocol-Version") != protocol
            or self.headers.get("MCP-Method") != "subscriptions/listen"
            or not accepts_event_stream(self.headers.get("Accept", ""))
        ):
            self.send_rpc(400, rpc_error(request_id, -32602, "subscriptions/listen requires the current protocol, MCP-Method header, and text/event-stream"), protocol)
            return
        if not isinstance(params, dict):
            self.send_rpc(400, rpc_error(request_id, -32602, "subscription params must be an object"), protocol)
            return
        notifications = params.get("notifications")
        metadata = params.get("_meta")
        if not isinstance(notifications, dict) or not isinstance(metadata, dict):
            self.send_rpc(400, rpc_error(request_id, -32602, "notifications and _meta must be objects"), protocol)
            return
        if metadata.get(PROTOCOL_VERSION_META_KEY) != protocol:
            self.send_rpc(400, rpc_error(request_id, -32602, "subscription protocol metadata must match MCP-Protocol-Version"), protocol)
            return

        list_filters = {}
        for key in ("toolsListChanged", "resourcesListChanged", "promptsListChanged"):
            if key in notifications:
                if not isinstance(notifications[key], bool):
                    self.send_rpc(400, rpc_error(request_id, -32602, "list-change filters must be booleans"), protocol)
                    return
                list_filters[key] = notifications[key]

        has_task_filter = "taskIds" in notifications
        requested_ids = notifications.get("taskIds", [])
        if not isinstance(requested_ids, list) or len(requested_ids) > MAX_TASK_SUBSCRIPTION_IDS:
            self.send_rpc(400, rpc_error(request_id, -32602, "notifications.taskIds must contain at most 64 task UUIDs"), protocol)
            return
        unique_ids = []
        seen = set()
        for task_id in requested_ids:
            if not isinstance(task_id, str) or not TASK_ID_PATTERN.fullmatch(task_id):
                self.send_rpc(400, rpc_error(request_id, -32602, "notifications.taskIds must contain at most 64 task UUIDs"), protocol)
                return
            normalized = task_id.lower()
            if normalized not in seen:
                seen.add(normalized)
                unique_ids.append(normalized)

        has_resource_filter = "resourceSubscriptions" in notifications
        requested_resource_uris = notifications.get("resourceSubscriptions", [])
        if (
            not isinstance(requested_resource_uris, list)
            or len(requested_resource_uris) > MAX_RESOURCE_SUBSCRIPTIONS
            or any(
                not valid_resource_update_uri(uri)
                for uri in requested_resource_uris
            )
        ):
            self.send_rpc(400, rpc_error(request_id, -32602, "notifications.resourceSubscriptions must contain at most 64 resource URIs"), protocol)
            return
        requested_resource_uris = list(dict.fromkeys(requested_resource_uris))

        if unique_ids:
            capabilities = metadata.get(CLIENT_CAPABILITIES_META_KEY)
            extensions = capabilities.get("extensions", {}) if isinstance(capabilities, dict) else {}
            task_capability = extensions.get(TASKS_EXTENSION_ID) if isinstance(extensions, dict) else None
            if (
                task_capability is None
                or task_capability is False
                or isinstance(task_capability, str) and not task_capability
                or isinstance(task_capability, (int, float)) and not isinstance(task_capability, bool) and task_capability == 0
            ):
                self.send_rpc(400, {
                    "jsonrpc": "2.0",
                    "id": request_id,
                    "error": {
                        "code": -32021,
                        "message": "Missing required client capability",
                        "data": {"requiredCapabilities": {"extensions": {TASKS_EXTENSION_ID: {}}}},
                    },
                }, protocol)
                return
        if not SUBSCRIPTION_STREAM_SLOTS.acquire(blocking=False):
            self.send_rpc(429, rpc_error(request_id, -32603, "subscription limit reached"), protocol)
            return

        runtime = self.server.task_runtime
        reserved_ids = 0
        task_subscription_reserved = False
        updates_queue = queue.Queue(maxsize=128)
        pending_resource_updates = set()
        pending_resource_updates_lock = threading.Lock()
        has_event_sources = False
        unsubscribe_task_updates = None
        unsubscribe_resource_updates = []
        try:
            requested_set = set(unique_ids)
            resource_task_ids = {}
            resource_uris_by_task_id = {}
            generic_resource_uris = set()
            for uri in requested_resource_uris:
                if not self.server.authenticator.can_access_resource(uri, granted_scopes):
                    continue
                if not uri.startswith("task://tasks/"):
                    if configured_resource_uri(uri):
                        generic_resource_uris.add(uri)
                    continue
                task_id = task_id_from_uri(uri)
                if not task_id or runtime is None:
                    continue
                try:
                    _wire, _task = runtime.get(task_id, granted_scopes, self.server.authenticator, granted_scopes)
                except Exception:
                    # Do not acknowledge task URIs the caller cannot read.
                    continue
                resource_task_ids[uri] = task_id
                resource_uris_by_task_id.setdefault(task_id, []).append(uri)

            all_task_ids = list(unique_ids)
            combined_seen = set(all_task_ids)
            for uri in requested_resource_uris:
                task_id = resource_task_ids.get(uri)
                if task_id and task_id not in combined_seen:
                    combined_seen.add(task_id)
                    all_task_ids.append(task_id)
            if len(all_task_ids) > MAX_TASK_SUBSCRIPTION_IDS:
                self.send_rpc(400, rpc_error(request_id, -32602, "task and resource subscription filters exceed 64 distinct task IDs"), protocol)
                return
            if runtime is not None and all_task_ids:
                if not runtime.reserve_task_subscription():
                    self.send_rpc(429, rpc_error(request_id, -32603, "task subscription limit reached"), protocol)
                    return
                task_subscription_reserved = True

            current = {}
            accepted_ids = []
            if runtime is not None:
                for task_id in all_task_ids:
                    try:
                        wire, _task = runtime.get(task_id, granted_scopes, self.server.authenticator, granted_scopes)
                    except TaskNotFound:
                        continue
                    except Exception:
                        self.send_rpc(500, rpc_error(request_id, -32603, "could not read task subscription state"), protocol)
                        return
                    detail = {key: value for key, value in wire.items() if key != "resultType"}
                    fingerprint = json.dumps(detail, sort_keys=True, separators=(",", ":"), allow_nan=False)
                    current[task_id] = {"detail": detail, "fingerprint": fingerprint}
                    accepted_ids.append(task_id)

            accepted_set = set(accepted_ids)
            accepted_resource_uris = [
                uri for uri in requested_resource_uris
                if uri in generic_resource_uris or resource_task_ids.get(uri) in accepted_set
            ]
            has_generic_resource_subscriptions = any(uri in generic_resource_uris for uri in accepted_resource_uris)
            if self.subscription_auth_expired(granted_scopes):
                self.send_rpc(401, rpc_error(request_id, -32000, "authentication expired during subscription setup"), protocol)
                return
            if accepted_ids and runtime is not None:
                if not runtime.reserve_watched_task_ids(len(accepted_ids)):
                    self.send_rpc(429, rpc_error(request_id, -32603, "watched task limit reached"), protocol)
                    return
                reserved_ids = len(accepted_ids)

            def enqueue_catalog_update():
                try:
                    updates_queue.put_nowait(("catalog", None))
                except queue.Full:
                    # The periodic fingerprint check below recovers a dropped hint.
                    pass

            if any(list_filters.values()):
                unsubscribe_resource_updates.append(MCP_CATALOG.subscribe(enqueue_catalog_update))
                has_event_sources = True

            list_fingerprints = {}
            try:
                for key, enabled in list_filters.items():
                    if enabled:
                        list_fingerprints[key] = subscription_catalog_fingerprint(
                            key, self.server.authenticator, granted_scopes, runtime,
                        )
            except (TypeError, ValueError):
                self.send_rpc(500, rpc_error(request_id, -32603, "could not encode MCP catalog state"), protocol)
                return

            def enqueue_resource_update(uri):
                if (not uri.startswith("task://tasks/")
                        and (not configured_resource_uri(uri)
                             or not self.server.authenticator.can_access_resource(uri, granted_scopes))):
                    return
                with pending_resource_updates_lock:
                    if uri in pending_resource_updates:
                        return
                    pending_resource_updates.add(uri)
                try:
                    updates_queue.put_nowait(("resource", uri))
                except queue.Full:
                    with pending_resource_updates_lock:
                        pending_resource_updates.discard(uri)

            try:
                if runtime is not None and accepted_ids:
                    _task_updates, unsubscribe_task_updates = runtime.subscribe_task_changes(list(accepted_ids), updates_queue)
                    has_event_sources = True
                for uri in accepted_resource_uris:
                    unsubscribe = RESOURCE_UPDATE_ADAPTER.subscribe(uri, lambda _uri, uri=uri: enqueue_resource_update(uri))
                    if callable(unsubscribe):
                        unsubscribe_resource_updates.append(unsubscribe)
                    has_event_sources = True
            except Exception:
                self.send_rpc(500, rpc_error(request_id, -32603, "could not establish resource update subscription"), protocol)
                return

            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache, no-transform")
            self.send_header("Transfer-Encoding", "chunked")
            self.send_header("Connection", "keep-alive")
            self.send_header("X-Accel-Buffering", "no")
            self.send_header("MCP-Protocol-Version", protocol)
            self.end_headers()
            self.wfile.flush()

            if self.subscription_auth_expired(granted_scopes):
                self.close_sse_stream()
                return
            acknowledged = {}
            if has_task_filter and unique_ids:
                acknowledged["taskIds"] = [task_id for task_id in unique_ids if task_id in accepted_set]
            if accepted_resource_uris:
                acknowledged["resourceSubscriptions"] = accepted_resource_uris
            for key, enabled in list_filters.items():
                if enabled:
                    acknowledged[key] = True
            self.write_sse_message({
                "jsonrpc": "2.0",
                "method": "notifications/subscriptions/acknowledged",
                "params": {
                    "notifications": acknowledged,
                    "_meta": {SUBSCRIPTION_ID_META_KEY: request_id},
                },
            })
            for task_id in unique_ids:
                if task_id in current:
                    self.write_subscription_task(request_id, current[task_id]["detail"])

            if not accepted_ids and not list_fingerprints and not has_generic_resource_subscriptions:
                self.finish_task_subscription(request_id)
                return

            poll_interval = max(5.0, runtime.poll_interval_ms / 1000 if runtime is not None else 5.0)
            next_poll = time.monotonic() + poll_interval
            next_keepalive = time.monotonic() + TASK_SUBSCRIPTION_KEEPALIVE_SECONDS

            def refresh_task(task_id):
                nonlocal reserved_ids
                if runtime is None or task_id not in current:
                    return True
                try:
                    wire, _task = runtime.get(task_id, granted_scopes, self.server.authenticator, granted_scopes)
                except TaskNotFound:
                    current.pop(task_id, None)
                    accepted_ids.remove(task_id)
                    runtime.release_watched_task_ids(1)
                    reserved_ids -= 1
                    return True
                except Exception:
                    return True
                detail = {key: value for key, value in wire.items() if key != "resultType"}
                fingerprint = json.dumps(detail, sort_keys=True, separators=(",", ":"), allow_nan=False)
                if current[task_id]["fingerprint"] == fingerprint:
                    return True
                current[task_id] = {"detail": detail, "fingerprint": fingerprint}
                if self.subscription_auth_expired(granted_scopes):
                    self.close_sse_stream()
                    return False
                if task_id in requested_set:
                    self.write_subscription_task(request_id, detail)
                for uri in resource_uris_by_task_id.get(task_id, ()):
                    self.write_subscription_resource_updated(request_id, uri)
                return True

            def refresh_lists():
                for key, previous in list_fingerprints.items():
                    try:
                        fingerprint = subscription_catalog_fingerprint(
                            key, self.server.authenticator, granted_scopes, runtime,
                        )
                    except (TypeError, ValueError):
                        continue
                    if fingerprint == previous:
                        continue
                    list_fingerprints[key] = fingerprint
                    self.write_subscription_list_changed(request_id, key)
                return True

            while True:
                if self.client_disconnected():
                    return
                if self.subscription_auth_expired(granted_scopes):
                    self.close_sse_stream()
                    return
                now = time.monotonic()
                if now >= next_poll:
                    for task_id in list(accepted_ids):
                        if not refresh_task(task_id):
                            return
                    refresh_lists()
                    next_poll = now + poll_interval
                    if not accepted_ids and not list_fingerprints and not has_generic_resource_subscriptions:
                        self.finish_task_subscription(request_id)
                        return
                if now >= next_keepalive:
                    self.write_sse_comment("keepalive")
                    next_keepalive = now + TASK_SUBSCRIPTION_KEEPALIVE_SECONDS
                wait_time = max(0.0, min(1.0, next_poll - time.monotonic(), next_keepalive - time.monotonic()))
                if wait_time and self.client_disconnected():
                    return
                if wait_time and has_event_sources:
                    try:
                        update = updates_queue.get(timeout=wait_time)
                    except queue.Empty:
                        continue
                    if isinstance(update, tuple) and len(update) == 2 and update[0] == "resource":
                        resource_uri = update[1]
                        with pending_resource_updates_lock:
                            pending_resource_updates.discard(resource_uri)
                        if self.subscription_auth_expired(granted_scopes):
                            self.close_sse_stream()
                            return
                        self.write_subscription_resource_updated(request_id, resource_uri)
                    elif isinstance(update, tuple) and len(update) == 2 and update[0] == "catalog":
                        if self.subscription_auth_expired(granted_scopes):
                            self.close_sse_stream()
                            return
                        refresh_lists()
                    elif not refresh_task(update):
                        return
                    if not accepted_ids and not list_fingerprints and not has_generic_resource_subscriptions:
                        self.finish_task_subscription(request_id)
                        return
                elif wait_time and not self.wait_for_client(wait_time):
                    return
        except OSError:
            return
        finally:
            if unsubscribe_task_updates is not None:
                try:
                    unsubscribe_task_updates()
                except Exception:
                    pass
            for unsubscribe in unsubscribe_resource_updates:
                try:
                    unsubscribe()
                except Exception:
                    pass
            if task_subscription_reserved and runtime is not None:
                runtime.release_task_subscription(reserved_ids)
            SUBSCRIPTION_STREAM_SLOTS.release()

    def write_subscription_resource_updated(self, request_id, uri):
        self.write_sse_message({
            "jsonrpc": "2.0",
            "method": "notifications/resources/updated",
            "params": {
                "uri": uri,
                "_meta": {SUBSCRIPTION_ID_META_KEY: request_id},
            },
        })

    def write_subscription_list_changed(self, request_id, key):
        methods = {
            "toolsListChanged": "notifications/tools/list_changed",
            "resourcesListChanged": "notifications/resources/list_changed",
            "promptsListChanged": "notifications/prompts/list_changed",
        }
        method = methods[key]
        self.write_sse_message({
            "jsonrpc": "2.0",
            "method": method,
            "params": {"_meta": {SUBSCRIPTION_ID_META_KEY: request_id}},
        })

    def subscription_auth_expired(self, granted_scopes):
        expires_at = getattr(granted_scopes, "expires_at", None)
        return expires_at is not None and time.time() >= expires_at

    def write_subscription_task(self, request_id, detail):
        params = dict(detail)
        params["_meta"] = {SUBSCRIPTION_ID_META_KEY: request_id}
        self.write_sse_message({"jsonrpc": "2.0", "method": "notifications/tasks", "params": params})

    def finish_task_subscription(self, request_id):
        self.write_sse_message(rpc_response(request_id, {
            "resultType": "complete",
            "_meta": {
                SUBSCRIPTION_ID_META_KEY: request_id,
                SERVER_INFO_META_KEY: {"name": "gregale-mcp-server", "version": "1.0.0"},
            },
        }))
        self.close_sse_stream()

    def close_sse_stream(self):
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()

    def mark_protocol_failure(self, reason):
        observation = getattr(self, "_protocol_observation", None)
        if observation:observation.failed(reason)

    def client_disconnected(self):
        try:
            readable, _, _ = select.select([self.connection], [], [], 0)
            if not readable:
                return False
            disconnected = self.connection.recv(1, socket.MSG_PEEK) == b""
            if disconnected:
                observation = getattr(self, "_protocol_observation", None)
                if observation:observation.failed("cancelled")
            return disconnected
        except (BlockingIOError, InterruptedError):
            return False
        except OSError:
            self.mark_protocol_failure("cancelled")
            return True

    def wait_for_client(self, duration):
        if duration > 0:
            time.sleep(duration)
        return not self.client_disconnected()

    def write_sse_message(self, message):
        observation = getattr(self, "_protocol_observation", None)
        if observation:observation.response(message)
        payload = b"event: message\ndata: " + json.dumps(
            message, separators=(",", ":"), allow_nan=False
        ).encode("utf-8") + b"\n\n"
        self.wfile.write(f"{len(payload):X}\r\n".encode("ascii") + payload + b"\r\n")
        self.wfile.flush()

    def write_sse_comment(self, comment):
        payload = f": {comment}\n\n".encode("ascii")
        self.wfile.write(f"{len(payload):X}\r\n".encode("ascii") + payload + b"\r\n")
        self.wfile.flush()

    def reject_origin(self):
        origins=self.headers.get_all("Origin",[])
        if origins and (len(origins)!=1 or origins[0] not in self.server.app_config.get("allowed_origins",[])):
            self.send_json(403, {"error": "origin_not_allowed"})
            return True
        return False

    def send_auth_failure(self, failure):
        headers = {"Cache-Control": "no-store"}
        authenticator = self.server.authenticator
        if authenticator.mode == "external-oauth" and failure.challenge:
            scopes = failure.scopes or authenticator.base_scopes
            challenge = (
                f'Bearer resource_metadata="{authenticator.metadata_url}", '
                f'scope="{" ".join(scopes)}"'
            )
            if failure.code != "authentication_required":
                challenge += f', error="{failure.code}"'
            headers["WWW-Authenticate"] = challenge
        self.send_json(failure.status, {"error": failure.code}, headers)

    def send_rpc(self, status, value, protocol=None):
        observation = getattr(self, "_protocol_observation", None)
        if observation:observation.response(value)
        headers = {}
        if protocol:
            headers["MCP-Protocol-Version"] = protocol
        self.send_json(status, protocol_response(value, protocol), headers)

    def send_json(self, status, value, extra_headers=None):
        body = json.dumps(value, separators=(",", ":"), allow_nan=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        for name, value in (extra_headers or {}).items():
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(body)

    def send_text(self, status, message):
        body = message.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def load_config(path):
    with open(path,"r",encoding="utf-8") as source:
        config=json.load(source,parse_constant=reject_json_constant)
    if not isinstance(config,dict) or type(config.get("version")) is not int or config["version"]!=1 or config.get("transport")!="streamable-http" or config.get("mode")!="stateless":
        raise AuthConfigurationError("expected one object with a supported MCP hosting profile")
    endpoint=config.get("endpoint")
    if not isinstance(endpoint,str) or not re.fullmatch(r"/(?:[A-Za-z0-9._~-]+/?)*",endpoint) or endpoint=="/healthz" or "//" in endpoint or any(part in {".",".."} for part in endpoint.split("/")):
        raise AuthConfigurationError("invalid MCP endpoint")
    origins=config.get("allowed_origins",[])
    if not isinstance(origins,list):raise AuthConfigurationError("invalid allowed origins")
    for origin in origins:
        try:
            parsed=urlsplit(origin)
            _=parsed.port
            if not isinstance(origin,str) or any(ch.isspace() for ch in origin) or parsed.scheme not in {"http","https"} or not parsed.hostname or parsed.username or parsed.path or parsed.query or parsed.fragment or parsed.netloc.endswith(":"):
                raise ValueError()
        except (ValueError,TypeError):
            raise AuthConfigurationError("invalid allowed origin") from None
    Authenticator(config)
    return config


def main():
    host = os.environ.get("MCP_BIND_ADDRESS")
    if not host:
        host = "0.0.0.0" if os.environ.get("FAAS_APP_ID") else "127.0.0.1"
    task_runtime = None
    try:
        config_path = os.environ.get("MCP_CONFIG_PATH", "gregale-mcp.json")
        app_config = load_config(config_path)
        if (
            not isinstance(app_config, dict)
            or isinstance(app_config.get("version"), bool)
            or app_config.get("version") != 1
            or app_config.get("transport") != "streamable-http"
            or app_config.get("mode") != "stateless"
        ):
            raise AuthConfigurationError("unsupported MCP hosting profile")
        task_role = resolve_task_role()
        expected_catalog_sha256 = ""
        if task_role in {"combined", "web"}:
            expected_catalog_sha256 = os.environ.get("MCP_CATALOG_EXPECTED_SHA256", "").strip().lower()
            if expected_catalog_sha256 and not re.fullmatch(r"[a-f0-9]{64}", expected_catalog_sha256):
                raise AuthConfigurationError("MCP_CATALOG_EXPECTED_SHA256 must be a 64-character SHA-256 hex digest")
        authenticator = Authenticator(app_config) if task_role in {"combined", "web"} else None
        task_runtime = DurableTaskRuntime(app_config, role=task_role)
        if not task_runtime.enabled:
            task_runtime.close()
            task_runtime = None
        if task_role in {"worker", "observer"}:
            stopped = threading.Event()
            signal.signal(signal.SIGTERM, lambda _signum, _frame: stopped.set())
            print(f"MCP task {task_role} process started", flush=True)
            try:
                while not stopped.wait(3600):
                    pass
            except KeyboardInterrupt:
                pass
            finally:
                if task_runtime is not None:
                    task_runtime.close()
            return
        port = int(os.environ.get("PORT", "8080"))
        server = DrainingHTTPServer((host, port), MCPHandler)
    except (ValueError, OSError, socket.error, json.JSONDecodeError, AuthConfigurationError) as exc:
        if task_runtime is not None:
            task_runtime.close()
        raise SystemExit(f"could not start MCP app: {exc}") from None
    server.app_config = app_config
    server.authenticator = authenticator
    server.task_runtime = task_runtime
    server.expected_catalog_sha256 = expected_catalog_sha256
    server.daemon_threads = True
    stop_heartbeat = start_protocol_heartbeat()
    def begin_shutdown():
        stop_heartbeat()
        server.draining.set()
        if task_runtime is not None:
            task_runtime.begin_drain()
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, lambda _signum, _frame: begin_shutdown())
    print(f"MCP server listening on {host}:{port}", flush=True)
    try:
        server.serve_forever(poll_interval=0.5)
    except KeyboardInterrupt:
        pass
    finally:
        stop_heartbeat()
        server.draining.set()
        raw_timeout = os.environ.get("MCP_DRAIN_TIMEOUT_SECONDS", "10")
        timeout = int(raw_timeout) if raw_timeout.isdecimal() and 1 <= int(raw_timeout) <= 300 else 10
        drain_thread = None
        if task_runtime is not None:
            task_runtime.begin_drain()
            drain_thread = threading.Thread(target=task_runtime.close, daemon=True)
            drain_thread.start()
        server.drain_connections(timeout)
        if drain_thread is not None:
            drain_thread.join(timeout=15)
        server.server_close()

if __name__ == "__main__":
    main()
