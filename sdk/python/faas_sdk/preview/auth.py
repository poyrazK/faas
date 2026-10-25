"""Verify application Auth access tokens in Python backends (ADR-1190).

Install the SDK with the extra that brings ES256 verification (see
https://gregale.dev/docs/sdks; ``faas_sdk`` on PyPI is not Gregale's)::

    pip install 'gregale-sdk[auth] @ git+https://github.com/poyrazK/faas@<commit>#subdirectory=sdk/python'

Then verify tokens and check permissions::

    from faas_sdk.preview.auth import AuthClient, bearer_token, resource

    auth = AuthClient("https://shop-auth.gregale.app")
    claims = auth.verify(bearer_token(request.headers.get("Authorization")))
    allowed = auth.authorize(token, "read", resource(org_id, "doc", doc_id))

``verify`` checks a token's ES256 signature against the Auth app's published
keys, its issuer, its audience and its expiry, without a network call once the
keys are cached. A token stays valid until it expires, at most 600 seconds
after a sign-out or ban. ``authorize`` asks the Auth app, which also checks
that the session is still active. Each method has an ``_async`` twin.

An API or MCP server that third-party OAuth clients call passes its resource
URL as ``audience``; ``Claims.client_id`` and ``Claims.scopes`` then name the
client and what its user granted.

A backend acting as an organization's service account gets its tokens with
the client-credentials grant::

    machine = ClientCredentials("https://shop-auth.gregale.app", client_id, client_secret)
    headers = {"Authorization": f"Bearer {machine.token()}"}
"""

from __future__ import annotations

import asyncio
import base64
import binascii
import ipaddress
import json
import re
import threading
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any
from urllib.parse import quote, urlsplit

import httpx

AUDIENCE = "authenticated"
# Clock-skew tolerance for exp and nbf, as Gregale's edge JWT rules use.
DEFAULT_LEEWAY = 60.0

# Gregale's own verifiers cache the key set for five minutes and refetch it
# for an unknown key ID at most once every 30 seconds, so a rotated-in key is
# learnt at once and a revoked key is dropped within five minutes.
_JWKS_TTL = 300.0
_JWKS_MIN_REFETCH = 30.0
_MAX_TOKEN_BYTES = 16 << 10
_MAX_BODY_BYTES = 64 << 10


class AuthError(Exception):
    """A token or Auth app failure. ``status`` and ``code`` are set when the
    Auth app's API refused a request."""

    def __init__(self, message: str, *, status: int | None = None, code: str | None = None) -> None:
        super().__init__(message)
        self.status = status
        self.code = code


class NoTokenError(AuthError):
    """The request carries no bearer token."""


class InvalidTokenError(AuthError):
    """The token is malformed, unsigned by the Auth app, for another issuer
    or audience, or expired; or the Auth app refused it (HTTP 401)."""


class TokenExpiredError(InvalidTokenError):
    """The token's only fault is its expiry."""


class AuthUnavailableError(AuthError):
    """The Auth app could not be reached, or answered 429 or 5xx."""


@dataclass(frozen=True)
class Organization:
    """A session's active organization, the user's role and its permissions."""

    id: str
    slug: str
    role: str
    permissions: tuple[str, ...] = ()


@dataclass(frozen=True)
class ServiceAccount:
    """A service account's token: the account and the secret it was issued for."""

    id: str
    secret_id: str


@dataclass(frozen=True)
class Claims:
    """A verified access token's claims. ``raw`` holds every claim."""

    subject: str
    expires_at: datetime
    session_id: str | None = None
    role: str | None = None
    email: str | None = None
    phone: str | None = None
    aal: str | None = None
    is_anonymous: bool = False
    issued_at: datetime | None = None
    # The third-party OAuth client the token was issued to, and the scopes
    # its user granted it; empty for the Auth app's own sessions.
    client_id: str | None = None
    scopes: tuple[str, ...] = ()
    # The session's active organization when the Auth app has organization
    # claims on; a changed membership shows at the next token.
    organization: Organization | None = None
    # Set while the app's support staff use the session as the user
    # (impersonation): who opened it, such as "admin:0A1B2C3D".
    actor: str | None = None
    # Set when the token belongs to an organization's service account (the
    # client-credentials grant), not a person: subject is the account's ID,
    # organization the account's and scopes what the token was narrowed to.
    # authorize applies the account's role and refuses as soon as the
    # account or its secret is revoked; verify alone accepts the token until
    # it expires.
    service_account: ServiceAccount | None = None
    app_metadata: Mapping[str, Any] = field(default_factory=dict)
    user_metadata: Mapping[str, Any] = field(default_factory=dict)
    raw: Mapping[str, Any] = field(default_factory=dict)


def resource(org: str, kind: str, name: str | int) -> str:
    """Name a resource in an organization the way authorization policies and
    ``gregale_auth.authorize()`` expect: ``org/<org id>/<kind>/<name>``."""
    return f"org/{org}/{kind}/{name}"


def bearer_token(authorization: str | None) -> str:
    """Return the token from an ``Authorization: Bearer`` header value."""
    scheme, _, token = (authorization or "").partition(" ")
    if scheme.lower() != "bearer" or not token:
        raise NoTokenError("no bearer token")
    return token


def _origin(auth_url: str) -> str:
    parts = urlsplit(auth_url.removesuffix("/"))
    if not parts.hostname or parts.username or parts.password or parts.path or parts.query or parts.fragment:
        raise ValueError(f"{auth_url!r} is not an Auth app origin such as https://shop-auth.gregale.app")
    if parts.scheme != "https" and not (parts.scheme == "http" and _loopback(parts.hostname)):
        raise ValueError(f"{auth_url!r} must use HTTPS")
    return f"{parts.scheme}://{parts.netloc}"


def _loopback(host: str) -> bool:
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def _b64decode(segment: str) -> bytes:
    try:
        return base64.b64decode(segment + "=" * (-len(segment) % 4), altchars=b"-_", validate=True)
    except (binascii.Error, ValueError) as exc:
        raise InvalidTokenError("malformed token") from exc


def _json_segment(segment: str) -> dict[str, Any]:
    try:
        value = json.loads(_b64decode(segment))
    except ValueError as exc:
        raise InvalidTokenError("malformed token") from exc
    if not isinstance(value, dict):
        raise InvalidTokenError("malformed token")
    return value


def _number(value: Any) -> float | None:
    return float(value) if isinstance(value, (int, float)) and not isinstance(value, bool) else None


def _actor(payload: Mapping[str, Any]) -> str | None:
    act = payload.get("act")
    sub = act.get("sub") if isinstance(act, dict) else None
    return sub if isinstance(sub, str) and sub else None


def _service_account(payload: Mapping[str, Any]) -> ServiceAccount | None:
    sub, secret = payload.get("sub"), payload.get("secret_id")
    if (
        payload.get("service_account") is not True
        or not isinstance(sub, str)
        or not isinstance(secret, str)
        or not secret
    ):
        return None
    return ServiceAccount(id=sub, secret_id=secret)


def _organization(payload: Mapping[str, Any]) -> Organization | None:
    org_id, slug, role = payload.get("org_id"), payload.get("org_slug"), payload.get("org_role")
    if not all(isinstance(value, str) and value for value in (org_id, slug, role)):
        return None
    permissions = payload.get("org_permissions")
    return Organization(
        id=org_id,
        slug=slug,
        role=role,
        permissions=tuple(p for p in permissions if isinstance(p, str)) if isinstance(permissions, list) else (),
    )


def _utc(seconds: float) -> datetime:
    return datetime.fromtimestamp(seconds, tz=timezone.utc)


def _ecdsa():
    try:
        from cryptography.exceptions import InvalidSignature
        from cryptography.hazmat.primitives import hashes
        from cryptography.hazmat.primitives.asymmetric import ec
        from cryptography.hazmat.primitives.asymmetric.utils import encode_dss_signature
    except ImportError as exc:  # pragma: no cover - exercised only without the extra
        raise ImportError("faas_sdk.preview.auth needs the 'auth' extra: install gregale-sdk[auth] as https://gregale.dev/docs/sdks shows") from exc
    return InvalidSignature, hashes, ec, encode_dss_signature


def _parse_keys(body: bytes) -> dict[str, Any]:
    _, _, ec, _ = _ecdsa()
    try:
        entries = json.loads(body)["keys"]
    except (ValueError, KeyError, TypeError) as exc:
        raise AuthUnavailableError("malformed key set") from exc
    keys: dict[str, Any] = {}
    for jwk in entries if isinstance(entries, list) else []:
        if not isinstance(jwk, dict) or jwk.get("kty") != "EC" or jwk.get("crv") != "P-256" or not jwk.get("kid"):
            continue
        if jwk.get("alg") not in (None, "ES256") or jwk.get("use") not in (None, "sig"):
            continue
        if "key_ops" in jwk and "verify" not in (jwk["key_ops"] or []):
            continue
        try:
            x, y = _b64decode(str(jwk.get("x", ""))), _b64decode(str(jwk.get("y", "")))
            if len(x) != 32 or len(y) != 32:
                continue
            numbers = ec.EllipticCurvePublicNumbers(int.from_bytes(x, "big"), int.from_bytes(y, "big"), ec.SECP256R1())
            keys[str(jwk["kid"])] = numbers.public_key()
        except (InvalidTokenError, ValueError):
            continue
    if not keys:
        raise AuthUnavailableError("key set has no ES256 keys")
    return keys


def _api_error(response: httpx.Response) -> AuthError:
    try:
        code = str(response.json().get("code") or "")
    except (ValueError, AttributeError):
        code = ""
    code = code or f"http_{response.status_code}"
    message = f"{code} (HTTP {response.status_code})"
    if response.status_code == 401:
        return InvalidTokenError(message, status=401, code=code)
    if response.status_code == 429 or response.status_code >= 500:
        return AuthUnavailableError(message, status=response.status_code, code=code)
    return AuthError(message, status=response.status_code, code=code)


class AuthClient:
    """Verifies tokens issued by one Auth app. Create one per Auth app and
    reuse it; it is safe to share between threads, and between tasks on one
    event loop."""

    def __init__(
        self,
        auth_url: str,
        *,
        leeway: float = DEFAULT_LEEWAY,
        audience: str = AUDIENCE,
        timeout: float = 10.0,
        http_client: httpx.Client | None = None,
        async_http_client: httpx.AsyncClient | None = None,
        clock: Callable[[], float] = time.time,
    ) -> None:
        if leeway < 0:
            raise ValueError("leeway must not be negative")
        if not audience:
            raise ValueError("audience must not be empty")
        # An API or MCP server that third-party OAuth clients call passes its
        # resource URL: the Auth app gives a client's token that audience.
        self.audience = audience
        self.origin = _origin(auth_url)
        self.issuer = self.origin + "/auth/v1"
        self._jwks_url = self.issuer + "/.well-known/jwks.json"
        self._leeway = leeway
        self._timeout = timeout
        self._http = http_client
        self._async_http = async_http_client
        self._owns_http = http_client is None
        self._owns_async_http = async_http_client is None
        self._clock = clock
        self._state = threading.Lock()
        self._fetch = threading.Lock()
        self._afetch: asyncio.Lock | None = None
        self._keys: dict[str, Any] = {}
        self._fetched_at = float("-inf")
        self._attempt_at = float("-inf")
        self._fetch_error: AuthError | None = None

    # -- verification -------------------------------------------------------

    def verify(self, token: str) -> Claims:
        """Verify ``token``; raises InvalidTokenError (TokenExpiredError for
        an expired one) or AuthUnavailableError when keys cannot be fetched."""
        header, kid = self._header(token)
        key = self._cached_key(kid)
        if key is None:
            self._refresh()
            key = self._known_key(kid)
        return self._check(token, key, header)

    async def verify_async(self, token: str) -> Claims:
        header, kid = self._header(token)
        key = self._cached_key(kid)
        if key is None:
            await self._refresh_async()
            key = self._known_key(kid)
        return self._check(token, key, header)

    def _header(self, token: str) -> tuple[dict[str, Any], str]:
        if not isinstance(token, str) or len(token) > _MAX_TOKEN_BYTES or token.count(".") != 2:
            raise InvalidTokenError("not a JWT")
        header = _json_segment(token.split(".", 1)[0])
        kid = header.get("kid")
        if header.get("alg") != "ES256" or not isinstance(kid, str) or not kid or "crit" in header:
            raise InvalidTokenError("header must name an ES256 key")
        return header, kid

    def _cached_key(self, kid: str) -> Any | None:
        """Return kid's key when the key set is fresh; None means refresh."""
        with self._state:
            if self._clock() - self._fetched_at < _JWKS_TTL:
                return self._keys.get(kid)
            return None

    def _known_key(self, kid: str) -> Any:
        with self._state:
            key = self._keys.get(kid)
            if key is not None:
                return key
            # No key at all: report why the refresh failed, if it did.
            error = self._fetch_error if not self._keys else None
        if error is not None:
            raise AuthUnavailableError(str(error), status=error.status, code=error.code)
        raise InvalidTokenError(f"unknown signing key {kid!r}")

    def _check(self, token: str, key: Any, header: dict[str, Any]) -> Claims:
        invalid_signature, hashes, ec, encode_dss_signature = _ecdsa()
        signing_input, _, signature64 = token.rpartition(".")
        signature = _b64decode(signature64)
        if len(signature) != 64:
            raise InvalidTokenError("malformed signature")
        der = encode_dss_signature(int.from_bytes(signature[:32], "big"), int.from_bytes(signature[32:], "big"))
        try:
            key.verify(der, signing_input.encode(), ec.ECDSA(hashes.SHA256()))
        except invalid_signature as exc:
            raise InvalidTokenError("bad signature") from exc
        return self._claims(_json_segment(signing_input.split(".")[1]))

    def _claims(self, payload: dict[str, Any]) -> Claims:
        now = self._clock()
        aud = payload.get("aud")
        exp, nbf, iat = _number(payload.get("exp")), _number(payload.get("nbf")), _number(payload.get("iat"))
        sub = payload.get("sub")
        if payload.get("iss") != self.issuer:
            raise InvalidTokenError(f"issuer {payload.get('iss')!r} is not {self.issuer!r}")
        if not (aud == self.audience or (isinstance(aud, list) and self.audience in aud)):
            raise InvalidTokenError(f"audience is not {self.audience!r}")
        if not isinstance(sub, str) or not sub:
            raise InvalidTokenError("no subject")
        if exp is None:
            raise InvalidTokenError("no expiry")
        if now > exp + self._leeway:
            raise TokenExpiredError("expired")
        if "nbf" in payload and (nbf is None or now + self._leeway < nbf):
            raise InvalidTokenError("not valid yet")

        def text(name: str) -> str | None:
            value = payload.get(name)
            return value if isinstance(value, str) else None

        def mapping(name: str) -> Mapping[str, Any]:
            value = payload.get(name)
            return value if isinstance(value, dict) else {}

        return Claims(
            subject=sub,
            expires_at=_utc(exp),
            session_id=text("session_id"),
            role=text("role"),
            email=text("email"),
            phone=text("phone"),
            aal=text("aal"),
            is_anonymous=payload.get("is_anonymous") is True,
            issued_at=_utc(iat) if iat is not None else None,
            client_id=text("client_id"),
            scopes=tuple((text("scope") or "").split()),
            organization=_organization(payload),
            actor=_actor(payload),
            service_account=_service_account(payload),
            app_metadata=mapping("app_metadata"),
            user_metadata=mapping("user_metadata"),
            raw=payload,
        )

    # -- key set ------------------------------------------------------------

    def _due(self) -> bool:
        with self._state:
            return self._clock() - self._attempt_at >= _JWKS_MIN_REFETCH

    def _record(self, keys: dict[str, Any] | None, error: AuthError | None) -> None:
        # A failed refresh keeps the cached keys working.
        with self._state:
            self._attempt_at = self._clock()
            self._fetch_error = error
            if keys is not None:
                self._keys, self._fetched_at = keys, self._attempt_at

    def _refresh(self) -> None:
        with self._fetch:
            if not self._due():
                return
            try:
                self._record(_parse_keys(self._get_jwks()), None)
            except AuthError as exc:
                self._record(None, exc)

    async def _refresh_async(self) -> None:
        if self._afetch is None:
            self._afetch = asyncio.Lock()
        async with self._afetch:
            if not self._due():
                return
            try:
                body = await self._get_jwks_async()
                self._record(_parse_keys(body), None)
            except AuthError as exc:
                self._record(None, exc)

    def _get_jwks(self) -> bytes:
        client = self._sync_client()
        try:
            with client.stream(
                "GET", self._jwks_url, headers={"Accept": "application/json"}, follow_redirects=False
            ) as resp:
                _check_status(resp)
                body = bytearray()
                for chunk in resp.iter_bytes():
                    body += chunk
                    _check_size(body)
                return bytes(body)
        except httpx.HTTPError as exc:
            raise AuthUnavailableError(f"fetch keys: {exc}") from exc

    async def _get_jwks_async(self) -> bytes:
        client = self._async_client()
        try:
            async with client.stream(
                "GET", self._jwks_url, headers={"Accept": "application/json"}, follow_redirects=False
            ) as resp:
                _check_status(resp)
                body = bytearray()
                async for chunk in resp.aiter_bytes():
                    body += chunk
                    _check_size(body)
                return bytes(body)
        except httpx.HTTPError as exc:
            raise AuthUnavailableError(f"fetch keys: {exc}") from exc

    # -- authorization ------------------------------------------------------

    def authorize(self, token: str, action: str, resource: str) -> bool:
        """Ask the Auth app whether the token's user may take ``action`` on
        ``resource`` (see :func:`resource`). The Auth app checks that the
        session is still active, so a signed-out or banned user is refused
        at once."""
        try:
            response = self._sync_client().post(
                self.origin + "/authz/v1/authorize",
                json={"action": action, "resource": resource},
                headers={"Authorization": f"Bearer {token}"},
                follow_redirects=False,
            )
        except httpx.HTTPError as exc:
            raise AuthUnavailableError(str(exc)) from exc
        return _allowed(response)

    async def authorize_async(self, token: str, action: str, resource: str) -> bool:
        try:
            response = await self._async_client().post(
                self.origin + "/authz/v1/authorize",
                json={"action": action, "resource": resource},
                headers={"Authorization": f"Bearer {token}"},
                follow_redirects=False,
            )
        except httpx.HTTPError as exc:
            raise AuthUnavailableError(str(exc)) from exc
        return _allowed(response)

    # -- clients ------------------------------------------------------------

    def _sync_client(self) -> httpx.Client:
        with self._state:
            if self._http is None:
                self._http = httpx.Client(timeout=self._timeout)
            return self._http

    def _async_client(self) -> httpx.AsyncClient:
        with self._state:
            if self._async_http is None:
                self._async_http = httpx.AsyncClient(timeout=self._timeout)
            return self._async_http

    def close(self) -> None:
        """Close the HTTP client this AuthClient created, if any."""
        if self._owns_http and self._http is not None:
            self._http.close()
            self._http = None

    async def aclose(self) -> None:
        """Close both HTTP clients this AuthClient created, if any."""
        self.close()
        if self._owns_async_http and self._async_http is not None:
            await self._async_http.aclose()
            self._async_http = None


def _check_status(resp: httpx.Response) -> None:
    if resp.status_code != 200:
        raise AuthUnavailableError(f"fetch keys: HTTP {resp.status_code}")


def _check_size(body: bytearray) -> None:
    if len(body) > _MAX_BODY_BYTES:
        raise AuthUnavailableError("fetch keys: key set too large")


def _allowed(response: httpx.Response) -> bool:
    if response.status_code != 200:
        raise _api_error(response)
    try:
        return response.json().get("allowed") is True
    except (ValueError, AttributeError) as exc:
        raise AuthError("malformed response from /authz/v1/authorize") from exc


_SERVICE_ACCOUNT_ID = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
_SERVICE_ACCOUNT_SECRET = re.compile(r"^gasa_[A-Za-z0-9_-]{43}$")
# A token is fetched again this long before it expires, so a request that
# carries it still finds it valid.
_CREDENTIALS_REFRESH_MARGIN = 60.0


class ClientCredentials:
    """Gets access tokens for an organization's service account with the
    client-credentials grant, for a backend calling an API or the Data API as
    that organization. ``token`` caches one until a minute before it expires;
    concurrent callers share one request. Keep the secret out of browsers."""

    def __init__(
        self,
        auth_url: str,
        client_id: str,
        client_secret: str,
        *,
        scope: tuple[str, ...] | list[str] = (),
        timeout: float = 10.0,
        http_client: httpx.Client | None = None,
        clock: Callable[[], float] = time.time,
    ) -> None:
        if not _SERVICE_ACCOUNT_ID.match(client_id):
            raise ValueError("client_id must be a service account ID")
        if not _SERVICE_ACCOUNT_SECRET.match(client_secret):
            raise ValueError("client_secret must be a service account secret")
        self.token_url = _origin(auth_url) + "/auth/v1/oauth/token"
        self._auth = (quote(client_id, safe=""), quote(client_secret, safe=""))
        self._form = {"grant_type": "client_credentials", **({"scope": " ".join(scope)} if scope else {})}
        self._timeout = timeout
        self._http = http_client
        self._owns_http = http_client is None
        self._clock = clock
        self._lock = threading.Lock()
        self._token: str | None = None
        self._refresh_at = float("-inf")

    def token(self) -> str:
        """Return a valid access token, fetching a new one when needed. A
        refusal raises InvalidTokenError (invalid_client), AuthError
        (invalid_scope) or AuthUnavailableError."""
        with self._lock:
            if self._token is not None and self._clock() < self._refresh_at:
                return self._token
            if self._http is None:
                self._http = httpx.Client(timeout=self._timeout)
            try:
                response = self._http.post(
                    self.token_url,
                    data=self._form,
                    auth=self._auth,
                    headers={"Accept": "application/json"},
                    follow_redirects=False,
                )
            except httpx.HTTPError as exc:
                raise AuthUnavailableError(str(exc)) from exc
            try:
                body = response.json()
            except ValueError:
                body = {}
            body = body if isinstance(body, dict) else {}
            token, expires_in = body.get("access_token"), _number(body.get("expires_in"))
            if response.status_code != 200 or not isinstance(token, str) or not token or not expires_in:
                code = body.get("error") if isinstance(body.get("error"), str) else f"http_{response.status_code}"
                status = response.status_code if response.status_code != 200 else 502
                message = f"{code} (HTTP {status})"
                if status == 401:
                    raise InvalidTokenError(message, status=status, code=code)
                if status == 429 or status >= 500:
                    raise AuthUnavailableError(message, status=status, code=code)
                raise AuthError(message, status=status, code=code)
            self._token = token
            self._refresh_at = self._clock() + expires_in - _CREDENTIALS_REFRESH_MARGIN
            return token

    def clear(self) -> None:
        """Drop the cached token, for example after an API answered 401."""
        with self._lock:
            self._token = None

    def close(self) -> None:
        """Close the HTTP client this object created, if any."""
        if self._owns_http and self._http is not None:
            self._http.close()
            self._http = None


__all__ = [
    "AUDIENCE",
    "DEFAULT_LEEWAY",
    "AuthClient",
    "AuthError",
    "AuthUnavailableError",
    "Claims",
    "ClientCredentials",
    "InvalidTokenError",
    "NoTokenError",
    "Organization",
    "ServiceAccount",
    "TokenExpiredError",
    "bearer_token",
    "resource",
]
