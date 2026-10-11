"""Application-owned OAuth validation and MCP catalog scope policy."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlsplit

import jwt
import anyio
from jwt import PyJWKClient
from mcp.shared.uri_template import UriTemplate
from mcp.shared.exceptions import MCPError
from mcp_types import (
    ListPromptsResult,
    ListResourcesResult,
    ListResourceTemplatesResult,
    ListToolsResult,
)
from starlette.responses import JSONResponse
from starlette.types import ASGIApp, Receive, Scope, Send


MAX_REQUEST_BODY_SIZE = 1024 * 1024
SCOPE_RE = re.compile(r"^[\x21\x23-\x5b\x5d-\x7e]+$")
KEY_CONTROL_RE = re.compile(r"[\x00-\x20\x7f]")
SAMPLE_RECORD_OWNERS = {
    "example-1": "demo-caller-a",
    "example-2": "demo-caller-b",
    "example-3": "demo-caller-a",
}


@dataclass(frozen=True)
class Principal:
    scopes: frozenset[str]
    subject: str
    client_id: str
    expires_at: int


@dataclass(frozen=True)
class CallerIdentity:
    """Minimal identity from a successfully verified bearer token."""

    subject: str
    client_id: str


def verified_caller_identity(principal: Principal | None) -> CallerIdentity | None:
    if principal is None or not principal.subject:
        return None
    return CallerIdentity(subject=principal.subject, client_id=principal.client_id)


def can_read_sample_record(record_id: str, auth_mode: str, caller: CallerIdentity | None) -> bool:
    owner = SAMPLE_RECORD_OWNERS.get(record_id)
    if owner is None:
        return False
    return auth_mode == "open" or (caller is not None and owner == caller.subject)


class ScopePolicy:
    def __init__(self, auth: dict[str, Any], field: str, *, resource: bool = False):
        self.field = field
        self.resource = resource
        self.configured = field in auth
        self.rules: dict[str, tuple[str, ...]] = {}
        self.templates: list[tuple[str, UriTemplate]] = []
        if not self.configured:
            return
        raw = auth[field]
        if not isinstance(raw, dict):
            raise RuntimeError(f"auth.{field} must be an object when configured")
        for name, scopes in raw.items():
            if not isinstance(name, str) or not name or KEY_CONTROL_RE.search(name):
                raise RuntimeError(f"auth.{field} keys must be nonempty and contain no ASCII whitespace or controls")
            if resource:
                self._validate_resource_key(name)
            self.rules[name] = validate_scopes(scopes, f"auth.{field}.{name}")
            if auth["mode"] == "open" and self.rules[name]:
                raise RuntimeError(f"Scoped {field} entries require external-oauth")
            if resource and "{" in name:
                self.templates.append((name, UriTemplate.parse(name)))

    @staticmethod
    def _validate_resource_key(value: str) -> None:
        if "\\" in value or KEY_CONTROL_RE.search(value):
            raise RuntimeError("Resource policy keys must be absolute URIs or URI templates without whitespace or controls")
        try:
            UriTemplate.parse(value)
            expanded = re.sub(r"\{[^{}]+\}", "placeholder", value)
            parsed = urlsplit(expanded)
            _ = parsed.port
        except (ValueError, TypeError) as exc:
            raise RuntimeError("Resource policy keys must be absolute URIs or valid URI templates") from exc
        if not parsed.scheme or parsed.username is not None or parsed.password is not None or parsed.fragment:
            raise RuntimeError("Resource policy keys must be absolute URIs or valid URI templates")
        if "{" in value or "}" in value:
            try:
                UriTemplate.parse(value)
            except ValueError as exc:
                raise RuntimeError("Resource policy keys must be absolute URIs or valid URI templates") from exc

    def required_scopes(self, identifier: str) -> tuple[tuple[str, ...] | None, bool]:
        if not self.configured:
            return (), True
        exact = self.rules.get(identifier)
        if exact is not None:
            return exact, True
        if not self.resource:
            return None, False
        found = False
        required: set[str] = set()
        for pattern, template in self.templates:
            if template.match(identifier) is None:
                continue
            found = True
            required.update(self.rules[pattern])
        return (tuple(sorted(required)), True) if found else (None, False)

    def can_access(self, identifier: str, principal: Principal | None, mode: str, endpoint_scopes: tuple[str, ...]) -> bool:
        required, listed = self.required_scopes(identifier)
        if not listed:
            return False
        if mode == "open":
            return True
        if principal is None:
            return False
        return set(endpoint_scopes).union(required or ()).issubset(principal.scopes)

    def all_scopes(self) -> set[str]:
        return {scope for scopes in self.rules.values() for scope in scopes}


def validate_scopes(value: Any, label: str) -> tuple[str, ...]:
    if not isinstance(value, list) or any(not isinstance(item, str) or not SCOPE_RE.fullmatch(item) for item in value):
        raise RuntimeError(f"{label} must be an explicit array of nonempty ASCII scope tokens")
    return tuple(dict.fromkeys(value))


def validate_auth_config(config: dict[str, Any]) -> None:
    auth = config.get("auth")
    if not isinstance(auth, dict) or auth.get("mode") not in {"open", "external-oauth"}:
        raise RuntimeError("Set auth.mode to open or external-oauth")
    allowed = {"mode", "issuer", "jwks_url", "resource", "scopes", "tool_scopes", "resource_scopes", "prompt_scopes"}
    if set(auth) - allowed:
        raise RuntimeError("auth contains unsupported fields")
    tools = ScopePolicy(auth, "tool_scopes")
    resources = ScopePolicy(auth, "resource_scopes", resource=True)
    prompts = ScopePolicy(auth, "prompt_scopes")
    scopes = validate_scopes(auth.get("scopes", []), "auth.scopes")
    if auth["mode"] == "open":
        if auth.get("issuer") or auth.get("jwks_url") or auth.get("resource") or scopes:
            raise RuntimeError("Open auth must not contain OAuth settings")
        return
    for key in ("issuer", "jwks_url", "resource"):
        value = auth.get(key)
        if not isinstance(value, str):
            raise RuntimeError(f"auth.{key} must be an HTTPS URL")
        try:
            parsed = urlsplit(value)
            _ = parsed.port
        except ValueError as exc:
            raise RuntimeError(f"auth.{key} must be an HTTPS URL") from exc
        if parsed.scheme != "https" or not parsed.hostname or parsed.username is not None or parsed.password is not None or parsed.query or parsed.fragment:
            raise RuntimeError(f"auth.{key} must be HTTPS without credentials, query or fragment")
    if urlsplit(auth["resource"]).path != config["endpoint"]:
        raise RuntimeError("auth.resource must identify the MCP endpoint")
    if not scopes:
        raise RuntimeError("Set nonempty OAuth scopes")


class AuthorizationPolicy:
    def __init__(self, config: dict[str, Any]):
        validate_auth_config(config)
        self.endpoint = config["endpoint"]
        self.auth = config["auth"]
        self.mode = self.auth["mode"]
        self.scopes = tuple(self.auth.get("scopes", []))
        self.tools = ScopePolicy(self.auth, "tool_scopes")
        self.resources = ScopePolicy(self.auth, "resource_scopes", resource=True)
        self.prompts = ScopePolicy(self.auth, "prompt_scopes")
        self.jwks = None
        self.metadata_path = None
        self.metadata = None
        if self.mode == "external-oauth":
            self.jwks = PyJWKClient(self.auth["jwks_url"], cache_jwk_set=True, lifespan=300, timeout=5)
            resource = urlsplit(self.auth["resource"])
            self.metadata_path = "/.well-known/oauth-protected-resource" + self.endpoint
            scopes_supported = set(self.scopes)
            scopes_supported.update(self.tools.all_scopes())
            scopes_supported.update(self.resources.all_scopes())
            scopes_supported.update(self.prompts.all_scopes())
            self.metadata = {
                "resource": self.auth["resource"],
                "authorization_servers": [self.auth["issuer"]],
                "scopes_supported": sorted(scopes_supported),
                "bearer_methods_supported": ["header"],
            }
            self.metadata_origin = f"https://{resource.netloc}"

    def _challenge(self, scopes: tuple[str, ...], error: str | None) -> str:
        metadata_url = self.metadata_origin + self.metadata_path
        value = f'Bearer resource_metadata="{metadata_url}", scope="{" ".join(scopes)}"'
        if error:
            value += f', error="{error}"'
        return value

    def _rejection(self, status: int, error: str, scopes: tuple[str, ...] | None = None) -> JSONResponse:
        headers = {"Cache-Control": "no-store"}
        if self.mode == "external-oauth" and error in {"authentication_required", "invalid_token", "insufficient_scope"}:
            headers["WWW-Authenticate"] = self._challenge(scopes or self.scopes, error if error != "authentication_required" else "")
        return JSONResponse({"error": error}, status_code=status, headers=headers)

    def authenticate(self, authorization: str | None) -> tuple[Principal | None, JSONResponse | None]:
        if self.mode == "open":
            return None, None
        parts = (authorization or "").split()
        if len(parts) != 2 or parts[0].lower() != "bearer":
            return None, self._rejection(401, "authentication_required")
        try:
            assert self.jwks is not None
            signing_key = self.jwks.get_signing_key_from_jwt(parts[1]).key
            payload = jwt.decode(
                parts[1],
                signing_key,
                algorithms=["RS256", "ES256"],
                issuer=self.auth["issuer"],
                audience=self.auth["resource"],
                options={"require": ["exp", "sub"]},
            )
            subject = payload.get("sub")
            if not isinstance(subject, str) or not subject:
                raise ValueError("invalid subject")
            claim = payload.get("scope")
            granted = frozenset(claim.split(" ")) if isinstance(claim, str) else frozenset()
            expires_at = int(payload["exp"])
            principal = Principal(
                scopes=granted,
                subject=subject,
                client_id=payload.get("client_id", "") if isinstance(payload.get("client_id", ""), str) else "",
                expires_at=expires_at,
            )
        except Exception:
            return None, self._rejection(401, "invalid_token")
        if not set(self.scopes).issubset(principal.scopes):
            return None, self._rejection(403, "insufficient_scope")
        return principal, None

    def authorize_rpc(self, message: Any, principal: Principal | None) -> JSONResponse | None:
        if not isinstance(message, dict):
            return None
        method = message.get("method")
        params = message.get("params")
        if not isinstance(params, dict):
            params = {}
        if method == "tools/call":
            policy, identifier, kind = self.tools, params.get("name"), "tool"
        elif method == "resources/read":
            policy, identifier, kind = self.resources, params.get("uri"), "resource"
        elif method == "prompts/get":
            policy, identifier, kind = self.prompts, params.get("name"), "prompt"
        elif method == "completion/complete":
            ref = params.get("ref")
            if not isinstance(ref, dict):
                return None
            if ref.get("type") == "ref/prompt":
                policy, identifier, kind = self.prompts, ref.get("name"), "prompt"
            elif ref.get("type") == "ref/resource":
                policy, identifier, kind = self.resources, ref.get("uri"), "resource"
            else:
                return None
        else:
            return None
        if not isinstance(identifier, str):
            return None
        required, listed = policy.required_scopes(identifier)
        if not listed:
            return self._rejection(403, f"{kind}_access_denied")
        if self.mode == "open":
            return None
        assert principal is not None
        all_required = tuple(dict.fromkeys((*self.scopes, *(required or ()))))
        if not set(all_required).issubset(principal.scopes):
            return self._rejection(403, "insufficient_scope", all_required)
        return None

    async def server_middleware(self, ctx, call_next):
        request = ctx.request
        state = request.scope.get("state", {}) if request is not None else {}
        principal = state.get("gregale_auth")
        params = ctx.params if isinstance(ctx.params, dict) else {}
        if ctx.method == "tools/call":
            policy, identifier, kind = self.tools, params.get("name"), "Tool"
        elif ctx.method == "resources/read":
            policy, identifier, kind = self.resources, params.get("uri"), "Resource"
        elif ctx.method == "prompts/get":
            policy, identifier, kind = self.prompts, params.get("name"), "Prompt"
        elif ctx.method == "completion/complete":
            ref = params.get("ref")
            if isinstance(ref, dict) and ref.get("type") == "ref/prompt":
                policy, identifier, kind = self.prompts, ref.get("name"), "Prompt"
            elif isinstance(ref, dict) and ref.get("type") == "ref/resource":
                policy, identifier, kind = self.resources, ref.get("uri"), "Resource"
            else:
                policy = None
                identifier = None
                kind = ""
        else:
            policy = None
            identifier = None
            kind = ""
        if policy is not None and isinstance(identifier, str) and not policy.can_access(identifier, principal, self.mode, self.scopes):
            raise MCPError(code=-32003, message=f"{kind} access denied")
        result = await call_next(ctx)
        if ctx.method == "completion/complete" and isinstance(result, dict):
            ref = params.get("ref")
            if isinstance(ref, dict) and ref.get("type") == "ref/resource" and ref.get("uri") == "customer://records/{recordId}":
                caller = verified_caller_identity(principal)
                completion = result.get("completion")
                if not isinstance(completion, dict) or not isinstance(completion.get("values"), list):
                    return result
                values = [
                    record_id
                    for record_id in completion["values"]
                    if isinstance(record_id, str) and can_read_sample_record(record_id, self.mode, caller)
                ]
                filtered = {**completion, "values": values, "total": len(values), "hasMore": False}
                return {**result, "completion": filtered}
        if ctx.method == "tools/list" and isinstance(result, ListToolsResult):
            return result.model_copy(update={"tools": [item for item in result.tools if self.tools.can_access(item.name, principal, self.mode, self.scopes)]})
        if ctx.method == "resources/list" and isinstance(result, ListResourcesResult):
            return result.model_copy(update={"resources": [item for item in result.resources if self.resources.can_access(item.uri, principal, self.mode, self.scopes)]})
        if ctx.method == "resources/templates/list" and isinstance(result, ListResourceTemplatesResult):
            return result.model_copy(update={"resource_templates": [item for item in result.resource_templates if self.resources.can_access(item.uri_template, principal, self.mode, self.scopes)]})
        if ctx.method == "prompts/list" and isinstance(result, ListPromptsResult):
            return result.model_copy(update={"prompts": [item for item in result.prompts if self.prompts.can_access(item.name, principal, self.mode, self.scopes)]})
        return result

    def asgi(self, app: ASGIApp) -> ASGIApp:
        return AuthorizationMiddleware(app, self)

    async def protected_resource_metadata(self, _request):
        return JSONResponse(self.metadata)


class AuthorizationMiddleware:
    def __init__(self, app: ASGIApp, policy: AuthorizationPolicy):
        self.app = app
        self.policy = policy

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http" or scope.get("path") != self.policy.endpoint:
            await self.app(scope, receive, send)
            return
        headers = {key.lower(): value for key, value in scope.get("headers", [])}
        authorization = headers.get(b"authorization", b"").decode("latin-1")
        if self.policy.mode == "open":
            principal, rejection = None, None
        else:
            principal, rejection = await anyio.to_thread.run_sync(self.policy.authenticate, authorization)
        if rejection is not None:
            await rejection(scope, receive, send)
            return
        buffered = b""
        if scope.get("method") == "POST":
            parts: list[bytes] = []
            size = 0
            while True:
                message = await receive()
                if message["type"] == "http.disconnect":
                    return
                chunk = message.get("body", b"")
                size += len(chunk)
                if size > MAX_REQUEST_BODY_SIZE:
                    response = JSONResponse({"error": "request_body_too_large"}, status_code=413, headers={"Cache-Control": "no-store"})
                    await response(scope, receive, send)
                    return
                parts.append(chunk)
                if not message.get("more_body", False):
                    break
            buffered = b"".join(parts)
            try:
                request_message = json.loads(buffered)
            except (UnicodeDecodeError, json.JSONDecodeError):
                request_message = None
            messages = request_message if isinstance(request_message, list) else [request_message]
            for item in messages:
                rejection = self.policy.authorize_rpc(item, principal)
                if rejection is not None:
                    await rejection(scope, receive, send)
                    return

        scope.setdefault("state", {})["gregale_auth"] = principal
        delivered = False

        async def replay_receive():
            nonlocal delivered
            if scope.get("method") == "POST" and not delivered:
                delivered = True
                return {"type": "http.request", "body": buffered, "more_body": False}
            return await receive()

        await self.app(scope, replay_receive, send)
