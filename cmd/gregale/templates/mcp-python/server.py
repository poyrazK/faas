import json
import os
import re
import atexit
import asyncio
from pathlib import Path
from typing import Any

import anyio
import uvicorn
from auth import AuthorizationPolicy, CallerIdentity, can_read_sample_record, validate_auth_config, verified_caller_identity
from mcp.server import MCPServer
from mcp.server.mcpserver import Context
from mcp.server.mcpserver.exceptions import ResourceNotFoundError
from mcp.shared.exceptions import MCPError
from mcp_types import CompleteResult, Completion, PromptReference, RequestParams, ResourceTemplateReference
from pydantic import Field
from starlette.middleware.cors import CORSMiddleware
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.routing import Route
from tasks import MCPTaskManager, TASK_EXTENSION_ID, TaskCapacityError, task_settings


CONFIG_FILE = Path(__file__).with_name("gregale-mcp.json")
SUMMARY_STYLES = ("brief", "technical", "executive")
SAMPLE_RECORD_IDS = ("example-1", "example-2", "example-3")
CUSTOMER_URI_TEMPLATE = "customer://records/{recordId}"


def load_config(path: Path = CONFIG_FILE) -> dict[str, Any]:
    try:
        config = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError("Could not read gregale-mcp.json") from exc
    if not isinstance(config, dict):
        raise RuntimeError("gregale-mcp.json must contain one object")
    endpoint = config.get("endpoint")
    if (
        config.get("version") != 1
        or config.get("transport") != "streamable-http"
        or config.get("mode") != "stateless"
        or not isinstance(endpoint, str)
        or not re.fullmatch(r"/(?:[A-Za-z0-9._~-]+/?)*", endpoint)
        or endpoint in {"/", "/healthz"}
        or endpoint.startswith("//")
        or any(char in endpoint for char in "\\?#")
        or any(part in {".", ".."} for part in endpoint.split("/"))
    ):
        raise RuntimeError("MCP config requires version 1, streamable-http, stateless mode, and a literal endpoint path")
    if config.get("legacy", False) is not False:
        raise RuntimeError("This starter does not implement legacy MCP transport; use the Node starter for legacy compatibility")
    validate_auth_config(config)
    tasks = config.get("tasks")
    if tasks is not None and not isinstance(tasks, dict):
        raise RuntimeError("tasks must be an object when configured")
    if isinstance(tasks, dict) and not isinstance(tasks.get("enabled", False), bool):
        raise RuntimeError("tasks.enabled must be true or false")
    origins = config.get("allowed_origins")
    if not isinstance(origins, list) or any(not isinstance(origin, str) or not is_exact_origin(origin) for origin in origins):
        raise RuntimeError("allowed_origins must be an array of exact HTTP origins")
    return config


def greet(name: str) -> str:
    """Greet a person by name."""
    name = name.strip()
    if not name or len(name) > 120:
        raise ValueError("name must contain between 1 and 120 characters")
    return f"Hello, {name}!"


def add(a: float, b: float) -> float:
    """Add two numbers."""
    return a + b


async def build_report(report: str, steps: int) -> str:
    """Build a harmless sample report; capable clients may receive it as a durable task."""
    report = report.strip()
    if not report or len(report) > 80 or isinstance(steps, bool) or not 1 <= steps <= 20:
        raise ValueError("report must contain 1-80 characters and steps must be between 1 and 20")
    for _ in range(steps):
        await asyncio.sleep(0.2)
    return f"Report {report} is ready after {steps} steps."


def welcome_resource() -> str:
    return "Welcome to the Gregale MCP starter."


def customer_record(recordId: str) -> str:
    return json.dumps({"recordId": recordId, "status": "example"}, separators=(",", ":"))


def caller_identity_for_request(context: Context) -> CallerIdentity | None:
    request = context.request_context.request
    scope = getattr(request, "scope", None)
    state = scope.get("state", {}) if isinstance(scope, dict) else {}
    return verified_caller_identity(state.get("gregale_auth"))


def summarize(text: str, style: str = "brief") -> list[dict[str, str]]:
    text = text.strip()
    if not text or len(text) > 4000:
        raise ValueError("text must contain between 1 and 4000 characters")
    instructions = {
        "brief": "Write a brief summary of the following text.",
        "technical": "Write a technical summary of the following text, preserving important details.",
        "executive": "Write an executive summary of the following text, emphasizing decisions and outcomes.",
    }
    if style not in instructions:
        raise ValueError("style must be brief, technical, or executive")
    return [{"role": "user", "content": f"{instructions[style]}\n\n{text}"}]


def origin_allowed(origin: str, allowed_origins: list[str]) -> bool:
    return not origin or origin in allowed_origins


def is_exact_origin(value: str) -> bool:
    from urllib.parse import urlsplit

    if not value or any(char.isspace() for char in value) or "\\" in value:
        return False
    try:
        parsed = urlsplit(value)
        # Accessing .port validates malformed and out-of-range port numbers.
        _ = parsed.port
    except ValueError:
        return False
    return (
        parsed.scheme in {"http", "https"}
        and bool(parsed.hostname)
        and bool(parsed.netloc)
        and not parsed.netloc.endswith(":")
        and parsed.username is None
        and parsed.password is None
        and not parsed.path
        and not parsed.query
        and not parsed.fragment
    )


def create_app(config: dict[str, Any] | None = None):
    config = config or load_config()
    policy = AuthorizationPolicy(config)
    tasks = MCPTaskManager(config) if task_settings(config) is not None else None
    if tasks is not None:
        atexit.register(tasks.close)
    server = MCPServer(name="gregale-mcp-python", version="1.0.0", middleware=[policy.server_middleware])
    server.tool()(greet)
    server.tool()(add)
    server.tool()(build_report)

    def read_customer_record(recordId: str, context: Context) -> str:
        caller = caller_identity_for_request(context)
        if not can_read_sample_record(recordId, config["auth"]["mode"], caller):
            raise ResourceNotFoundError("Resource not found")
        return customer_record(recordId)

    server.resource(
        "greeting://welcome", name="welcome", description="A public example resource.", mime_type="text/plain"
    )(welcome_resource)
    server.resource(
        "customer://records/{recordId}",
        name="customer_record",
        description="Demo records are restricted by the verified OAuth subject.",
        mime_type="application/json",
    )(read_customer_record)
    server.prompt("summarize", description="Create a summary prompt with a selected style.")(summarize)

    @server.completion()
    async def complete(ref, argument, _context):
        options: tuple[str, ...] = ()
        if isinstance(ref, PromptReference) and ref.name == "summarize" and argument.name == "style":
            options = SUMMARY_STYLES
        elif (
            isinstance(ref, ResourceTemplateReference)
            and ref.uri == CUSTOMER_URI_TEMPLATE
            and argument.name == "recordId"
        ):
            options = SAMPLE_RECORD_IDS
        prefix = argument.value.casefold()
        values = [option for option in options if option.casefold().startswith(prefix)]
        return Completion(values=values, total=len(values), has_more=False)

    if tasks is not None:
        install_task_extension(server, tasks, policy)

    app = server.streamable_http_app(
        streamable_http_path=config["endpoint"],
        stateless_http=True,
        host="0.0.0.0",
        max_request_body_size=1 * 1024 * 1024,
    )
    app.router.routes.insert(0, Route("/healthz", healthz, methods=["GET"]))
    if policy.metadata is not None:
        app.router.routes.insert(0, Route(policy.metadata_path, policy.protected_resource_metadata, methods=["GET"]))
    return OriginPolicy(policy.asgi(app), config["allowed_origins"])


class TaskGetParams(RequestParams):
    task_id: str = Field(alias="taskId")


class TaskUpdateParams(TaskGetParams):
    input_responses: dict[str, Any] = Field(default_factory=dict, alias="inputResponses")


def _request_principal(ctx):
    request = ctx.request
    scope = getattr(request, "scope", None)
    state = scope.get("state", {}) if isinstance(scope, dict) else {}
    return state.get("gregale_auth")


def _client_supports_tasks(ctx) -> bool:
    capabilities = ctx.session.client_capabilities
    extensions = capabilities.extensions if capabilities else None
    return bool(extensions and TASK_EXTENSION_ID in extensions)


def install_task_extension(server: MCPServer, tasks: MCPTaskManager, policy: AuthorizationPolicy) -> None:
    """Add the Task extension and its task methods to the public low-level SDK server."""
    lowlevel = server._lowlevel_server
    lowlevel.extensions[TASK_EXTENSION_ID] = {}
    call_handler = lowlevel.get_request_handler("tools/call")

    async def call_tool(ctx, params):
        if params.name == "build_report" and _client_supports_tasks(ctx):
            arguments = params.arguments or {}
            report = arguments.get("report") if isinstance(arguments, dict) else None
            steps = arguments.get("steps") if isinstance(arguments, dict) else None
            if not isinstance(report, str) or isinstance(steps, bool) or not isinstance(steps, int):
                raise MCPError(code=-32602, message="report and steps are required")
            if not report.strip() or len(report) > 80 or not 1 <= steps <= 20:
                raise MCPError(code=-32602, message="report must contain 1-80 characters and steps must be between 1 and 20")
            principal = _request_principal(ctx)
            if not policy.tools.can_access("build_report", principal, policy.mode, policy.scopes):
                raise MCPError(code=-32003, message="Tool access denied")
            try:
                return await anyio.to_thread.run_sync(tasks.create, {"report": report.strip(), "steps": steps}, principal)
            except TaskCapacityError as exc:
                raise MCPError(code=-32005, message="MCP task queue capacity reached") from exc
        return await call_handler.handler(ctx, params)

    async def get_task(ctx, params):
        principal = _request_principal(ctx)
        task = await anyio.to_thread.run_sync(tasks.get, params.task_id, principal)
        if task is None or not policy.tools.can_access(task["tool_name"], principal, policy.mode, policy.scopes):
            raise MCPError(code=-32004, message="Task not found")
        return tasks.wire_task(task)

    async def cancel_task(ctx, params):
        principal = _request_principal(ctx)
        task = await anyio.to_thread.run_sync(tasks.get, params.task_id, principal)
        if task is None or not policy.tools.can_access(task["tool_name"], principal, policy.mode, policy.scopes):
            raise MCPError(code=-32004, message="Task not found")
        if not await anyio.to_thread.run_sync(tasks.cancel, params.task_id, principal):
            raise MCPError(code=-32004, message="Task not found")
        return {"resultType": "complete"}

    async def update_task(ctx, params):
        principal = _request_principal(ctx)
        task = await anyio.to_thread.run_sync(tasks.get, params.task_id, principal)
        if task is None or not policy.tools.can_access(task["tool_name"], principal, policy.mode, policy.scopes):
            raise MCPError(code=-32004, message="Task not found")
        try:
            updated = await anyio.to_thread.run_sync(tasks.update, params.task_id, principal, params.input_responses)
        except ValueError as exc:
            raise MCPError(code=-32602, message=str(exc)) from exc
        if not updated:
            raise MCPError(code=-32004, message="Task not found")
        return {"resultType": "complete"}

    lowlevel.add_request_handler("tools/call", call_handler.params_type, call_tool)
    lowlevel.add_request_handler("tasks/get", TaskGetParams, get_task)
    lowlevel.add_request_handler("tasks/cancel", TaskGetParams, cancel_task)
    lowlevel.add_request_handler("tasks/update", TaskUpdateParams, update_task)


async def healthz(_request: Request) -> JSONResponse:
    return JSONResponse({"ok": True})


class OriginPolicy:
    def __init__(self, app, allowed_origins: list[str]):
        self.allowed_origins = frozenset(allowed_origins)
        self.app = CORSMiddleware(
            app,
            allow_origins=allowed_origins,
            allow_methods=["GET", "POST", "DELETE", "OPTIONS"],
            allow_headers=["*"],
        )

    async def __call__(self, scope, receive, send):
        if scope["type"] == "http":
            origin = next((value.decode("latin-1") for key, value in scope.get("headers", []) if key.lower() == b"origin"), "")
            if not origin_allowed(origin, self.allowed_origins):
                response = JSONResponse({"error": "Origin is not allowed"}, status_code=403)
                await response(scope, receive, send)
                return
        await self.app(scope, receive, send)


app = create_app()


def main() -> None:
    host = os.environ.get("MCP_BIND_ADDRESS") or ("0.0.0.0" if os.environ.get("FAAS_APP_ID") else "127.0.0.1")
    uvicorn.run(app, host=host, port=int(os.environ.get("PORT", "8080")), log_level="info")


if __name__ == "__main__":
    main()
