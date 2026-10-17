"""faas_sdk.auth: application Auth token verification (ADR-1190)."""

from __future__ import annotations

import base64
import json
from datetime import UTC, datetime

import httpx
import pytest

ec = pytest.importorskip("cryptography.hazmat.primitives.asymmetric.ec")
from cryptography.hazmat.primitives import hashes  # noqa: E402
from cryptography.hazmat.primitives.asymmetric.utils import decode_dss_signature  # noqa: E402
from faas_sdk.auth import (  # noqa: E402
    AuthClient,
    AuthError,
    AuthUnavailableError,
    InvalidTokenError,
    NoTokenError,
    TokenExpiredError,
    bearer_token,
    resource,
)

ORIGIN = "https://shop-auth.gregale.app"
ISSUER = ORIGIN + "/auth/v1"
USER = "0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70"
NOW = 1_800_000_000.0


def b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


class Key:
    def __init__(self, kid: str) -> None:
        self.kid = kid
        self.private = ec.generate_private_key(ec.SECP256R1())

    def jwk(self) -> dict:
        numbers = self.private.public_key().public_numbers()
        return {
            "kty": "EC",
            "crv": "P-256",
            "alg": "ES256",
            "kid": self.kid,
            "key_ops": ["verify"],
            "x": b64(numbers.x.to_bytes(32, "big")),
            "y": b64(numbers.y.to_bytes(32, "big")),
        }

    def sign(self, claims: dict, header: dict | None = None) -> str:
        header = header if header is not None else {"alg": "ES256", "typ": "JWT", "kid": self.kid}
        unsigned = b64(json.dumps(header).encode()) + "." + b64(json.dumps(claims).encode())
        r, s = decode_dss_signature(self.private.sign(unsigned.encode(), ec.ECDSA(hashes.SHA256())))
        return unsigned + "." + b64(r.to_bytes(32, "big") + s.to_bytes(32, "big"))


class Clock:
    def __init__(self) -> None:
        self.now = NOW

    def __call__(self) -> float:
        return self.now


class AuthApp:
    """Serves a JWKS and /authz/v1/authorize like the Auth app."""

    def __init__(self, *keys: Key) -> None:
        self.keys = [k.jwk() for k in keys]
        self.jwks_hits = 0
        self.jwks_status = 200
        self.authorize_status = 200
        self.authorize_body = '{"allowed": true}'
        self.requests: list[httpx.Request] = []

    def handle(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if request.url.path == "/auth/v1/.well-known/jwks.json":
            self.jwks_hits += 1
            if self.jwks_status != 200:
                return httpx.Response(self.jwks_status, text="down")
            return httpx.Response(200, json={"keys": self.keys})
        if request.url.path == "/authz/v1/authorize":
            return httpx.Response(self.authorize_status, text=self.authorize_body)
        return httpx.Response(404)

    def client(self, clock: Clock) -> AuthClient:
        transport = httpx.MockTransport(self.handle)
        return AuthClient(
            ORIGIN,
            clock=clock,
            http_client=httpx.Client(transport=transport),
            async_http_client=httpx.AsyncClient(transport=transport),
        )


def user_claims(clock: Clock, **changes) -> dict:
    claims = {
        "iss": ISSUER,
        "aud": "authenticated",
        "sub": USER,
        "session_id": "5c1b9d2e-0b5f-4d8f-8a3a-7a512f0e6f70",
        "role": "authenticated",
        "email": "ada@example.com",
        "aal": "aal1",
        "iat": int(clock.now),
        "exp": int(clock.now) + 600,
        "app_metadata": {"provider": "email"},
    }
    claims.update(changes)
    return {k: v for k, v in claims.items() if v is not None}


def test_verify_accepts_an_auth_app_token():
    key, clock = Key("0A1B2C3D"), Clock()
    auth = AuthApp(key).client(clock)
    claims = auth.verify(key.sign(user_claims(clock)))
    assert claims.subject == USER
    assert claims.email == "ada@example.com"
    assert claims.aal == "aal1"
    assert claims.role == "authenticated"
    assert claims.session_id == "5c1b9d2e-0b5f-4d8f-8a3a-7a512f0e6f70"
    assert claims.expires_at == datetime.fromtimestamp(NOW + 600, tz=UTC)
    assert claims.issued_at == datetime.fromtimestamp(NOW, tz=UTC)
    assert claims.app_metadata == {"provider": "email"}
    assert claims.raw["aal"] == "aal1"
    assert claims.is_anonymous is False


@pytest.mark.parametrize(
    ("name", "error"),
    [
        ("other issuer", InvalidTokenError),
        ("other audience", InvalidTokenError),
        ("audience list without ours", InvalidTokenError),
        ("no subject", InvalidTokenError),
        ("no expiry", InvalidTokenError),
        ("expired", TokenExpiredError),
        ("not valid yet", InvalidTokenError),
        ("service token", InvalidTokenError),
        ("other key", InvalidTokenError),
        ("tampered claims", InvalidTokenError),
        ("HS256", InvalidTokenError),
        ("none", InvalidTokenError),
        ("no key ID", InvalidTokenError),
        ("critical header", InvalidTokenError),
        ("two segments", InvalidTokenError),
        ("short signature", InvalidTokenError),
        ("oversized", InvalidTokenError),
        ("unknown key", InvalidTokenError),
    ],
)
def test_verify_refuses_tokens_it_should_not(name, error):
    key, other, clock = Key("0A1B2C3D"), Key("0A1B2C3D"), Clock()
    auth = AuthApp(key).client(clock)
    valid = key.sign(user_claims(clock))
    head, body, sig = valid.split(".")
    tokens = {
        "other issuer": key.sign(user_claims(clock, iss="https://evil.example/auth/v1")),
        "other audience": key.sign(user_claims(clock, aud="service")),
        "audience list without ours": key.sign(user_claims(clock, aud=["service"])),
        "no subject": key.sign({k: v for k, v in user_claims(clock).items() if k != "sub"}),
        "no expiry": key.sign({k: v for k, v in user_claims(clock).items() if k != "exp"}),
        "expired": key.sign(user_claims(clock, exp=int(NOW) - 61)),
        "not valid yet": key.sign(user_claims(clock, nbf=int(NOW) + 61)),
        "service token": key.sign({"role": "service_role", "iat": int(NOW), "exp": int(NOW) + 60}),
        "other key": other.sign(user_claims(clock)),
        "tampered claims": head + "." + b64(b'{"sub":"x"}') + "." + sig,
        "HS256": b64(b'{"alg":"HS256","kid":"0A1B2C3D"}') + "." + body + "." + sig,
        "none": b64(b'{"alg":"none"}') + "." + body + ".",
        "no key ID": key.sign(user_claims(clock), {"alg": "ES256"}),
        "critical header": key.sign(user_claims(clock), {"alg": "ES256", "kid": key.kid, "crit": ["b64"]}),
        "two segments": head + "." + body,
        "short signature": head + "." + body + "." + b64(b"short"),
        "oversized": "a" * (16 << 10) + "..",
        "unknown key": key.sign(user_claims(clock), {"alg": "ES256", "kid": "FFFFFFFF"}),
    }
    with pytest.raises(error):
        auth.verify(tokens[name])


def test_verify_tolerates_clock_skew():
    key, clock = Key("0A1B2C3D"), Clock()
    auth = AuthApp(key).client(clock)
    auth.verify(key.sign(user_claims(clock, exp=int(NOW) - 59, aud=["authenticated", "other"])))


def test_verify_learns_rotated_keys_and_forgets_revoked_ones():
    old, new, clock = Key("0A1B2C3D"), Key("1B2C3D4E"), Clock()
    app = AuthApp(old)
    auth = app.client(clock)
    auth.verify(old.sign(user_claims(clock)))
    # A rotation publishes the new key before it signs: an unknown key ID
    # refetches at once.
    app.keys = [old.jwk(), new.jwk()]
    clock.now += 31
    auth.verify(new.sign(user_claims(clock)))
    assert app.jwks_hits == 2
    # Unknown key IDs refetch at most once every 30 seconds.
    clock.now += 31
    for _ in range(5):
        with pytest.raises(InvalidTokenError):
            auth.verify(new.sign(user_claims(clock), {"alg": "ES256", "kid": "FFFFFFFF"}))
    assert app.jwks_hits == 3
    # Revoking the old key reaches the verifier when the cache expires.
    app.keys = [new.jwk()]
    auth.verify(old.sign(user_claims(clock)))
    clock.now += 300
    with pytest.raises(InvalidTokenError):
        auth.verify(old.sign(user_claims(clock)))


def test_verify_keeps_cached_keys_while_the_auth_app_is_down():
    key, clock = Key("0A1B2C3D"), Clock()
    app = AuthApp(key)
    auth = app.client(clock)
    app.jwks_status = 502
    with pytest.raises(AuthUnavailableError):
        auth.verify(key.sign(user_claims(clock)))
    app.jwks_status = 200
    clock.now += 30
    auth.verify(key.sign(user_claims(clock)))
    app.jwks_status = 502
    clock.now += 300
    auth.verify(key.sign(user_claims(clock)))


def test_unusable_keys_are_skipped():
    good, clock = Key("0A1B2C3D"), Clock()
    off_curve = {**good.jwk(), "kid": "BAD00001", "y": b64(bytes(32))}
    sign_only = {**good.jwk(), "kid": "BAD00002", "key_ops": ["sign"]}
    rsa = {"kty": "RSA", "kid": "BAD00003", "n": "AQAB", "e": "AQAB"}
    app = AuthApp()
    app.keys = [off_curve, sign_only, rsa]
    auth = app.client(clock)
    with pytest.raises(AuthUnavailableError):
        auth.verify(good.sign(user_claims(clock)))
    app.keys.append(good.jwk())
    clock.now += 30
    auth.verify(good.sign(user_claims(clock)))
    with pytest.raises(InvalidTokenError):
        auth.verify(good.sign(user_claims(clock), {"alg": "ES256", "kid": "BAD00002"}))


@pytest.mark.asyncio
async def test_verify_async_and_authorize_async():
    key, clock = Key("0A1B2C3D"), Clock()
    app = AuthApp(key)
    auth = app.client(clock)
    claims = await auth.verify_async(key.sign(user_claims(clock)))
    assert claims.subject == USER
    with pytest.raises(TokenExpiredError):
        await auth.verify_async(key.sign(user_claims(clock, exp=int(NOW) - 120)))
    assert await auth.authorize_async("tok", "read", resource("org-1", "doc", 42)) is True
    app.authorize_status, app.authorize_body = 401, '{"status":401,"code":"not_authenticated"}'
    with pytest.raises(InvalidTokenError) as raised:
        await auth.authorize_async("tok", "read", "org/org-1/doc/42")
    assert raised.value.code == "not_authenticated"
    await auth.aclose()


def test_authorize():
    clock = Clock()
    app = AuthApp()
    auth = app.client(clock)
    target = resource("6f1c0d1e-0000-4000-8000-000000000001", "doc", 42)
    assert target == "org/6f1c0d1e-0000-4000-8000-000000000001/doc/42"
    assert auth.authorize("tok", "read", target) is True
    request = app.requests[-1]
    assert request.method == "POST"
    assert str(request.url) == ORIGIN + "/authz/v1/authorize"
    assert request.headers["Authorization"] == "Bearer tok"
    assert json.loads(request.content) == {"action": "read", "resource": target}
    app.authorize_body = '{"allowed": false}'
    assert auth.authorize("tok", "write", target) is False
    for status, body, error, code in [
        (401, '{"status":401,"code":"not_authenticated"}', InvalidTokenError, "not_authenticated"),
        (503, '{"status":503,"code":"auth_unavailable"}', AuthUnavailableError, "auth_unavailable"),
        (429, "slow down", AuthUnavailableError, "http_429"),
        (400, '{"status":400,"code":"invalid_request"}', AuthError, "invalid_request"),
    ]:
        app.authorize_status, app.authorize_body = status, body
        with pytest.raises(error) as raised:
            auth.authorize("tok", "read", target)
        assert (raised.value.status, raised.value.code) == (status, code)
        if error is AuthError:
            assert not isinstance(raised.value, (InvalidTokenError, AuthUnavailableError))


def test_authorize_unreachable():
    def fail(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    auth = AuthClient(ORIGIN, http_client=httpx.Client(transport=httpx.MockTransport(fail)))
    with pytest.raises(AuthUnavailableError):
        auth.authorize("tok", "read", "org/o/doc/1")
    with pytest.raises(AuthUnavailableError):
        auth.verify(Key("0A1B2C3D").sign(user_claims(Clock())))


def test_redirects_are_not_followed():
    def redirect(request: httpx.Request) -> httpx.Response:
        return httpx.Response(302, headers={"Location": "https://evil.example/jwks.json"})

    auth = AuthClient(ORIGIN, http_client=httpx.Client(transport=httpx.MockTransport(redirect)))
    with pytest.raises(AuthUnavailableError):
        auth.verify(Key("0A1B2C3D").sign(user_claims(Clock())))


@pytest.mark.parametrize(
    ("url", "ok"),
    [
        ("https://shop-auth.gregale.app", True),
        ("https://shop-auth.gregale.app/", True),
        ("http://127.0.0.1:8080", True),
        ("http://localhost:8080", True),
        ("http://shop-auth.gregale.app", False),
        ("https://shop-auth.gregale.app/auth/v1", False),
        ("https://user@shop-auth.gregale.app", False),
        ("https://shop-auth.gregale.app?x=1", False),
        ("shop-auth.gregale.app", False),
        ("", False),
    ],
)
def test_auth_client_accepts_only_an_auth_app_origin(url, ok):
    if ok:
        assert AuthClient(url).issuer.endswith("/auth/v1")
        assert "//auth" not in AuthClient(url).issuer
    else:
        with pytest.raises(ValueError):
            AuthClient(url)


def test_bearer_token():
    assert bearer_token("Bearer abc") == "abc"
    assert bearer_token("bearer abc") == "abc"
    for value in (None, "", "Bearer", "Bearer ", "Basic abc"):
        with pytest.raises(NoTokenError):
            bearer_token(value)


def test_audience_accepts_an_oauth_clients_token():
    key, clock = Key("0A1B2C3D"), Clock()
    app = AuthApp(key)
    own = app.client(clock)
    resource = "https://shop-mcp.gregale.app/mcp"
    transport = httpx.MockTransport(app.handle)
    mcp = AuthClient(ORIGIN, clock=clock, audience=resource, http_client=httpx.Client(transport=transport))
    token = key.sign(user_claims(clock, aud=resource, client_id="6a1b2c3d-0000-4000-8000-000000000002", scope="openid email"))
    claims = mcp.verify(token)
    assert claims.client_id == "6a1b2c3d-0000-4000-8000-000000000002"
    assert claims.scopes == ("openid", "email")
    with pytest.raises(InvalidTokenError):
        own.verify(token)
    with pytest.raises(InvalidTokenError):
        mcp.verify(key.sign(user_claims(clock)))
    with pytest.raises(ValueError):
        AuthClient(ORIGIN, audience="")
