"""Runtime feature flag evaluation for managed Gregale workloads.

The client evaluates immutable configuration locally. Request identity is only
trusted when this module is used behind Gregale's gateway, which replaces the
reserved customer and flag-context headers.
"""

from __future__ import annotations

import asyncio
import base64
import binascii
import contextvars
import hashlib
import json
import os
import re
import time
from collections.abc import AsyncIterator, Iterable, Mapping
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any, Literal
from urllib.parse import parse_qsl, urlencode, urljoin, urlsplit, urlunsplit

import httpx

GREGALE_FLAG_EVIDENCE_HEADER = "X-Faas-Flag-Evidence"
GREGALE_FLAG_CONTEXT_HEADER = "X-Faas-Platform-Tenant-Id"
GREGALE_FLAG_PROPAGATION_HEADER = "X-Faas-Flag-Context"

_CUSTOMER_ID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
_UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", re.IGNORECASE)
_FLAG_KEY = re.compile(r"^[a-z][a-z0-9_-]{0,63}$")
_FALLBACK_REASONS = {"flag_missing", "configuration_stale", "type_mismatch"}
_DECISION_REASONS = _FALLBACK_REASONS | {"default", "disabled", "customer_missing", "rule_match"}
_MAX_SAFE_INTEGER = 9_007_199_254_740_991
_MAX_FLAGS = 100
_MAX_GROUPS = 100
_MAX_RULES = 32
_MAX_VARIANTS = 16
_MAX_CUSTOMERS = 1000
_MAX_EVIDENCE = 32
_MAX_BUNDLE_BYTES = 256 * 1024
_MAX_IDENTITY_BYTES = 16 * 1024
_MAX_PROPAGATION_BYTES = 8 * 1024
_MAX_PROPAGATION_HEADER_BYTES = 12 * 1024


@dataclass(frozen=True, slots=True)
class FlagDecision:
    """A flag value and the rule/configuration that selected it."""

    flag: str
    value: bool | str
    config_version: int
    reason: str
    source: Literal["configuration", "fallback", "inherited"]
    type_: Literal["variant"] | None = None
    rule_id: str | None = None
    bucket: int | None = None
    rollout_bucket: int | None = None
    inherited_from: dict[str, str] | None = None

    def to_dict(self) -> dict[str, Any]:
        result: dict[str, Any] = {
            "flag": self.flag,
            "value": self.value,
            "config_version": self.config_version,
            "reason": self.reason,
            "source": self.source,
        }
        if self.type_ is not None:
            result["type"] = self.type_
        if self.rule_id is not None:
            result["rule_id"] = self.rule_id
        if self.bucket is not None:
            result["bucket"] = self.bucket
        if self.rollout_bucket is not None:
            result["rollout_bucket"] = self.rollout_bucket
        if self.inherited_from is not None:
            result["inherited_from"] = dict(self.inherited_from)
        return result


@dataclass(slots=True)
class _RequestState:
    customer: str | None
    bundle: dict[str, Any] | None
    fresh: bool
    evidence: dict[str, dict[str, Any]]
    inherited: dict[str, tuple[dict[str, Any], dict[str, str]]]


def flag_bucket(seed: str, key: str, customer: str) -> int:
    """Return the stable 0..9999 customer allocation bucket."""
    digest = hashlib.sha256(f"{seed}\0{key}\0{customer}".encode()).digest()
    return int.from_bytes(digest[:4], "big") % 10_000


def flag_variant_bucket(seed: str, key: str, customer: str) -> int:
    """Return the separate stable bucket used for weighted variant assignment."""
    digest = hashlib.sha256(f"{seed}\0{key}\0variant\0{customer}".encode()).digest()
    return int.from_bytes(digest[:4], "big") % 10_000


def evaluate_flag(bundle: Mapping[str, Any], key: str, customer: str | None, fallback: bool) -> FlagDecision:
    """Evaluate a boolean flag without I/O using trusted customer identity."""
    version = _bundle_version(bundle)
    if not isinstance(fallback, bool):
        return FlagDecision(key, fallback, version, "type_mismatch", "fallback")
    flag = _find_flag(bundle, key)
    if flag is None:
        return FlagDecision(key, fallback, version, "flag_missing", "fallback")
    if flag.get("type") not in (None, "boolean"):
        return FlagDecision(key, fallback, version, "type_mismatch", "fallback")

    default = flag.get("default")
    if not isinstance(default, bool):
        return FlagDecision(key, fallback, version, "type_mismatch", "fallback")
    decision = FlagDecision(key, default, version, "default", "configuration")
    if not flag.get("enabled"):
        return _replace_decision(decision, reason="disabled")
    if not customer:
        return _replace_decision(decision, reason="customer_missing")

    groups = bundle.get("groups", {})
    for rule in flag.get("rules", []):
        customers = rule.get("customers")
        if customers and customer not in customers:
            continue
        group = rule.get("group")
        if group and customer not in groups.get(group, []):
            continue
        bucket = None
        rollout = rule.get("rollout")
        if rollout is not None:
            bucket = flag_bucket(flag["seed"], flag["key"], customer)
            if bucket >= rollout:
                continue
        value = rule.get("value")
        if not isinstance(value, bool):
            return FlagDecision(key, fallback, version, "type_mismatch", "fallback")
        return _replace_decision(
            decision,
            value=value,
            reason="rule_match",
            rule_id=rule["id"],
            bucket=bucket,
        )
    return decision


def evaluate_variant(bundle: Mapping[str, Any], key: str, customer: str | None, fallback: str) -> FlagDecision:
    """Evaluate a named variant with independent rollout and weight buckets."""
    version = _bundle_version(bundle)
    if not isinstance(fallback, str):
        return FlagDecision(key, fallback, version, "type_mismatch", "fallback", type_="variant")
    flag = _find_flag(bundle, key)
    if flag is None:
        return FlagDecision(key, fallback, version, "flag_missing", "fallback", type_="variant")
    if flag.get("type") != "variant":
        return FlagDecision(key, fallback, version, "type_mismatch", "fallback", type_="variant")

    default = flag.get("default")
    if not isinstance(default, str):
        return FlagDecision(key, fallback, version, "type_mismatch", "fallback", type_="variant")
    decision = FlagDecision(key, default, version, "default", "configuration", type_="variant")
    if not flag.get("enabled"):
        return _replace_decision(decision, reason="disabled")
    if not customer:
        return _replace_decision(decision, reason="customer_missing")

    groups = bundle.get("groups", {})
    for rule in flag.get("rules", []):
        customers = rule.get("customers")
        if customers and customer not in customers:
            continue
        group = rule.get("group")
        if group and customer not in groups.get(group, []):
            continue
        rollout_bucket = None
        rollout = rule.get("rollout")
        if rollout is not None:
            rollout_bucket = flag_bucket(flag["seed"], flag["key"], customer)
            if rollout_bucket >= rollout:
                continue
        value = rule.get("value")
        if value is not None:
            return _replace_decision(
                decision,
                value=value,
                reason="rule_match",
                rule_id=rule["id"],
                rollout_bucket=rollout_bucket,
            )
        bucket = flag_variant_bucket(flag["seed"], flag["key"], customer)
        selected = _choose_variant(flag["variants"], bucket)
        return _replace_decision(
            decision,
            value=selected,
            reason="rule_match",
            rule_id=rule["id"],
            bucket=bucket,
            rollout_bucket=rollout_bucket,
        )
    return decision


def _replace_decision(decision: FlagDecision, **changes: Any) -> FlagDecision:
    values = {
        "flag": decision.flag,
        "value": decision.value,
        "config_version": decision.config_version,
        "reason": decision.reason,
        "source": decision.source,
        "type_": decision.type_,
        "rule_id": decision.rule_id,
        "bucket": decision.bucket,
        "rollout_bucket": decision.rollout_bucket,
        "inherited_from": decision.inherited_from,
    }
    values.update(changes)
    return FlagDecision(**values)


def _find_flag(bundle: Mapping[str, Any], key: str) -> Mapping[str, Any] | None:
    flags = bundle.get("flags", [])
    for flag in flags:
        if flag.get("key") == key:
            return flag
    return None


def _bundle_version(bundle: Mapping[str, Any]) -> int:
    version = bundle.get("version", 0)
    return version if _is_int(version) and version >= 0 else 0


def _choose_variant(variants: list[dict[str, Any]], bucket: int) -> str:
    for variant in variants:
        if bucket < variant["weight"]:
            return variant["key"]
        bucket -= variant["weight"]
    return ""


def _is_int(value: Any) -> bool:
    return isinstance(value, int) and not isinstance(value, bool)


def _valid_key(value: Any) -> bool:
    return isinstance(value, str) and _FLAG_KEY.fullmatch(value) is not None


def _valid_customer_id(value: Any) -> bool:
    return isinstance(value, str) and _CUSTOMER_ID.fullmatch(value) is not None


def validate_bundle(raw: Any) -> dict[str, Any]:
    """Validate the bounded runtime response before storing it as a snapshot."""
    if not isinstance(raw, Mapping):
        raise ValueError("Invalid Flags bundle")
    environment_id = raw.get("environment_id")
    version = raw.get("version")
    flags = raw.get("flags")
    groups = raw.get("groups")
    if (
        not _valid_customer_id(environment_id)
        or not _is_int(version)
        or version < 0
        or version > _MAX_SAFE_INTEGER
        or not isinstance(flags, list)
        or len(flags) > _MAX_FLAGS
        or not isinstance(groups, Mapping)
        or len(groups) > _MAX_GROUPS
    ):
        raise ValueError("Invalid Flags bundle")

    normalized_groups: dict[str, list[str]] = {}
    for name, members in groups.items():
        if not _valid_key(name) or not _valid_ids(members):
            raise ValueError("Invalid Flags group")
        normalized_groups[name] = list(members)

    keys: set[str] = set()
    normalized_flags: list[dict[str, Any]] = []
    for raw_flag in flags:
        if not isinstance(raw_flag, Mapping):
            raise ValueError("Invalid Flags definition")
        flag = dict(raw_flag)
        key = flag.get("key")
        flag_type = flag.get("type")
        is_variant = flag_type == "variant"
        default = flag.get("default")
        seed = flag.get("seed")
        rules = flag.get("rules")
        if (
            not _valid_key(key)
            or key in keys
            or ("type" in flag and flag_type not in ("boolean", "variant"))
            or not isinstance(flag.get("enabled"), bool)
            or (not isinstance(default, str) if is_variant else not isinstance(default, bool))
            or not isinstance(seed, str)
            or not seed
            or len(seed.encode()) > 128
            or "\0" in seed
            or not isinstance(rules, list)
            or len(rules) > _MAX_RULES
        ):
            raise ValueError("Invalid Flags definition")
        keys.add(key)

        variant_keys: set[str] = set()
        if is_variant:
            variants = flag.get("variants")
            if not isinstance(variants, list) or not 2 <= len(variants) <= _MAX_VARIANTS:
                raise ValueError("Invalid Flags variants")
            total_weight = 0
            for variant in variants:
                if not isinstance(variant, Mapping):
                    raise ValueError("Invalid Flags variant")
                variant_key = variant.get("key")
                weight = variant.get("weight")
                if (
                    not _valid_key(variant_key)
                    or variant_key in variant_keys
                    or not _is_int(weight)
                    or not 0 <= weight <= 10_000
                ):
                    raise ValueError("Invalid Flags variant")
                variant_keys.add(variant_key)
                total_weight += weight
            if total_weight != 10_000 or default not in variant_keys:
                raise ValueError("Invalid Flags variant weights or default")
        elif "variants" in flag:
            raise ValueError("Boolean flag cannot define variants")

        rule_ids: set[str] = set()
        for rule in rules:
            if not isinstance(rule, Mapping):
                raise ValueError("Invalid Flags rule")
            rule_id = rule.get("id")
            customers = rule.get("customers")
            group = rule.get("group")
            rollout = rule.get("rollout")
            value = rule.get("value")
            valid_rule_value = (
                "value" not in rule or (isinstance(value, str) and value in variant_keys)
                if is_variant
                else isinstance(value, bool)
            )
            if (
                not _valid_key(rule_id)
                or rule_id in rule_ids
                or not valid_rule_value
                or ("customers" in rule and not _valid_ids(customers))
                or ("group" in rule and (not _valid_key(group) or group not in normalized_groups))
                or ("rollout" in rule and (not _is_int(rollout) or not 0 <= rollout <= 10_000))
                or (not customers and not group and rollout is None)
            ):
                raise ValueError("Invalid Flags rule")
            rule_ids.add(rule_id)
        normalized_flags.append(flag)

    return {
        "environment_id": environment_id,
        "version": version,
        "flags": normalized_flags,
        "groups": normalized_groups,
    }


def _valid_ids(value: Any) -> bool:
    return (
        isinstance(value, list)
        and len(value) <= _MAX_CUSTOMERS
        and all(_valid_customer_id(member) for member in value)
        and len(set(value)) == len(value)
    )


def _header_text(value: str | bytes) -> str:
    return value.decode("latin-1").strip() if isinstance(value, bytes) else str(value).strip()


def _single_header(headers: Any, header_name: str) -> str:
    target = header_name.lower()
    values: list[str] = []
    if isinstance(headers, Mapping):
        rows: Iterable[tuple[Any, Any]] = headers.items()
    else:
        rows = headers
    for name, value in rows:
        if _header_text(name).lower() != target or value is None:
            continue
        if isinstance(value, (list, tuple)):
            values.extend(_header_text(item) for item in value if item is not None)
        else:
            values.append(_header_text(value))
    return ", ".join(values)


def _b64url_encode(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def _b64url_decode(value: str, maximum_bytes: int, maximum_length: int) -> bytes:
    if not value or len(value) > maximum_length or re.fullmatch(r"[A-Za-z0-9_-]+", value) is None:
        raise ValueError("Invalid flag context")
    raw = base64.b64decode(value + "=" * (-len(value) % 4), altchars=b"-_", validate=True)
    if not raw or len(raw) > maximum_bytes or _b64url_encode(raw) != value:
        raise ValueError("Invalid flag context")
    return raw


def _decode_propagation(value: str) -> tuple[str, dict[str, tuple[dict[str, Any], dict[str, str]]]] | None:
    if not value or len(value) > _MAX_PROPAGATION_HEADER_BYTES:
        return None
    try:
        envelope = json.loads(_b64url_decode(value, _MAX_PROPAGATION_BYTES, _MAX_PROPAGATION_HEADER_BYTES))
        if (
            not isinstance(envelope, dict)
            or set(envelope) != {"version", "customer_id", "decisions"}
            or type(envelope.get("version")) is not int
            or envelope["version"] != 1
            or not isinstance(envelope.get("customer_id"), str)
            or _UUID.fullmatch(envelope["customer_id"]) is None
            or not isinstance(envelope.get("decisions"), list)
            or not 1 <= len(envelope["decisions"]) <= _MAX_EVIDENCE
        ):
            return None
        decisions: dict[str, tuple[dict[str, Any], dict[str, str]]] = {}
        allowed_decision_keys = {
            "flag",
            "value",
            "type",
            "config_version",
            "rule_id",
            "reason",
            "bucket",
            "rollout_bucket",
            "source",
            "origin",
        }
        for decision in envelope["decisions"]:
            if not isinstance(decision, dict) or not set(decision) <= allowed_decision_keys:
                return None
            flag = decision.get("flag")
            version = decision.get("config_version")
            reason = decision.get("reason")
            source = decision.get("source")
            is_variant = decision.get("type") == "variant"
            if (
                not _valid_key(flag)
                or flag in decisions
                or not _is_int(version)
                or not 0 <= version <= _MAX_SAFE_INTEGER
                or reason not in _DECISION_REASONS
                or source not in ("configuration", "fallback")
                or ("type" in decision and decision.get("type") not in ("boolean", "variant"))
                or (
                    not isinstance(decision.get("value"), str) or not _valid_key(decision["value"])
                    if is_variant
                    else not isinstance(decision.get("value"), bool)
                )
            ):
                return None
            rule_id = decision.get("rule_id")
            if rule_id is not None and not _valid_key(rule_id):
                return None
            for bucket_name in ("bucket", "rollout_bucket"):
                bucket = decision.get(bucket_name)
                if bucket is not None and (not _is_int(bucket) or not 0 <= bucket < 10_000):
                    return None
            if (source == "fallback") != (reason in _FALLBACK_REASONS):
                return None
            if reason == "rule_match":
                if rule_id is None:
                    return None
            elif (
                rule_id is not None or decision.get("bucket") is not None or decision.get("rollout_bucket") is not None
            ):
                return None
            origin = decision.get("origin")
            if (
                not isinstance(origin, dict)
                or set(origin) != {"app_id", "environment_id"}
                or not isinstance(origin.get("app_id"), str)
                or _UUID.fullmatch(origin["app_id"]) is None
                or not isinstance(origin.get("environment_id"), str)
                or _UUID.fullmatch(origin["environment_id"]) is None
            ):
                return None
            normalized = dict(decision)
            if normalized.get("type") == "boolean":
                normalized.pop("type")
            decisions[flag] = (normalized, origin)
        return envelope["customer_id"].lower(), decisions
    except (ValueError, TypeError, UnicodeDecodeError, binascii.Error, json.JSONDecodeError):
        return None


class GregaleFlags:
    """Async, server-side feature flag client for managed Gregale workloads.

    Use GregaleFlagsMiddleware in an ASGI application to pin one
    configuration snapshot per request and attach response evidence.
    """

    def __init__(
        self,
        api_url: str,
        identity_endpoint: str | None = None,
        *,
        refresh_ms: int = 15_000,
        max_stale_ms: int = 60_000,
        timeout_ms: int = 2_000,
        async_client: httpx.AsyncClient | None = None,
        now: Any = None,
    ) -> None:
        api = urlsplit(api_url)
        if api.scheme != "https" or not api.hostname or api.username or api.password:
            raise ValueError("Flags API requires an HTTPS URL")
        endpoint = identity_endpoint or os.environ.get("FAAS_WORKLOAD_IDENTITY_ENDPOINT", "")
        identity = urlsplit(endpoint)
        if (
            identity.scheme != "http"
            or identity.hostname not in {"127.0.0.1", "::1", "localhost"}
            or identity.username
            or identity.password
            or not identity.path
        ):
            raise ValueError("Workload identity endpoint must be loopback HTTP")
        if not (
            isinstance(max_stale_ms, int)
            and not isinstance(max_stale_ms, bool)
            and 0 < max_stale_ms <= 60_000
            and isinstance(refresh_ms, int)
            and not isinstance(refresh_ms, bool)
            and 0 < refresh_ms <= max_stale_ms
            and isinstance(timeout_ms, int)
            and not isinstance(timeout_ms, bool)
            and 0 < timeout_ms <= 10_000
        ):
            raise ValueError("Invalid Flags refresh or timeout bounds")

        query = [(name, value) for name, value in parse_qsl(identity.query) if name != "audience"]
        query.append(("audience", "gregale:flags"))
        self._identity_url = urlunsplit((identity.scheme, identity.netloc, identity.path, urlencode(query), ""))
        self._api_url = urljoin(api_url.rstrip("/") + "/", "/v1/runtime/flags")
        self._refresh_ms = refresh_ms
        self._max_stale_ms = max_stale_ms
        self._timeout_ms = timeout_ms
        self._now = now or (lambda: time.time() * 1000)
        self._client = async_client
        self._owns_client = async_client is None
        self._bundle: dict[str, Any] | None = None
        self._refreshed_at: float | None = None
        self._refresh_lock = asyncio.Lock()
        self._request: contextvars.ContextVar[_RequestState | None] = contextvars.ContextVar(
            f"gregale_flags_request_{id(self)}", default=None
        )
        self._refresh_task: asyncio.Task[None] | None = None

    async def __aenter__(self) -> GregaleFlags:
        await self.start()
        return self

    async def __aexit__(self, *_: Any) -> None:
        await self.close()

    async def start(self) -> None:
        """Best-effort initial refresh, then refresh configuration in the background."""
        try:
            await self.refresh()
        except Exception:
            pass
        if self._refresh_task is None or self._refresh_task.done():
            self._refresh_task = asyncio.create_task(self._refresh_loop())

    async def close(self) -> None:
        """Stop background refresh and close an internally owned HTTP client."""
        task = self._refresh_task
        self._refresh_task = None
        if task is not None:
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass
        if self._owns_client and self._client is not None:
            await self._client.aclose()
            self._client = None

    async def refresh(self) -> None:
        """Fetch a fresh runtime bundle using the guest's scoped workload identity."""
        async with self._refresh_lock:
            await self._load()

    async def _refresh_if_stale(self) -> None:
        if self._bundle is not None and self._is_fresh(self._now()):
            return
        async with self._refresh_lock:
            if self._bundle is not None and self._is_fresh(self._now()):
                return
            await self._load()

    async def _refresh_loop(self) -> None:
        while True:
            await asyncio.sleep(self._refresh_ms / 1000)
            try:
                await self.refresh()
            except asyncio.CancelledError:
                raise
            except Exception:
                continue

    def _http_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(
                timeout=self._timeout_ms / 1000,
                follow_redirects=False,
                trust_env=False,
            )
        return self._client

    async def _get_json(self, url: str, maximum_bytes: int, headers: dict[str, str] | None = None) -> Any:
        client = self._http_client()
        content = bytearray()
        async with client.stream(
            "GET",
            url,
            headers=headers,
            timeout=self._timeout_ms / 1000,
            follow_redirects=False,
        ) as response:
            if not response.is_success:
                raise RuntimeError(f"Flags request failed ({response.status_code})")
            async for chunk in response.aiter_bytes():
                content.extend(chunk)
                if len(content) > maximum_bytes:
                    raise ValueError("Flags response too large")
        return json.loads(content)

    async def _load(self) -> None:
        identity = await self._get_json(self._identity_url, _MAX_IDENTITY_BYTES)
        if not isinstance(identity, Mapping):
            raise ValueError("Invalid workload identity")
        token = identity.get("access_token")
        if not isinstance(token, str) or not token or len(token) > 8192:
            raise ValueError("Invalid workload identity")
        raw_bundle = await self._get_json(
            self._api_url,
            _MAX_BUNDLE_BYTES + 1024,
            {"Authorization": f"Bearer {token}", "Accept": "application/json", "Cache-Control": "no-store"},
        )
        bundle = validate_bundle(raw_bundle)
        if self._bundle is not None and (
            bundle["environment_id"] != self._bundle["environment_id"] or bundle["version"] < self._bundle["version"]
        ):
            raise ValueError("Flags configuration scope or version regressed")
        self._bundle = bundle
        self._refreshed_at = self._now()

    def _is_fresh(self, now: float) -> bool:
        if self._refreshed_at is None:
            return False
        age = now - self._refreshed_at
        return 0 <= age <= self._max_stale_ms

    @asynccontextmanager
    async def run_request(self, headers: Any) -> AsyncIterator[GregaleFlags]:
        """Pin one bundle and trusted customer context for an ASGI request."""
        try:
            await self._refresh_if_stale()
        except Exception:
            pass
        now = self._now()
        fresh = self._bundle is not None and self._is_fresh(now)
        raw_customer = _single_header(headers, GREGALE_FLAG_CONTEXT_HEADER)
        propagation = _decode_propagation(_single_header(headers, GREGALE_FLAG_PROPAGATION_HEADER))
        if propagation is not None and raw_customer and raw_customer.lower() != propagation[0]:
            propagation = None
        customer = (
            propagation[0]
            if propagation is not None
            else raw_customer
            if _CUSTOMER_ID.fullmatch(raw_customer) is not None
            else None
        )
        request = _RequestState(
            customer=customer,
            bundle=self._bundle,
            fresh=fresh,
            evidence={},
            inherited=propagation[1] if propagation is not None else {},
        )
        token = self._request.set(request)
        try:
            yield self
        finally:
            self._request.reset(token)

    def boolean(self, key: str, fallback: bool) -> FlagDecision:
        """Evaluate a boolean flag inside run_request."""
        request = self._current_request()
        prior = request.evidence.get(key)
        if prior is not None:
            if isinstance(prior["value"], bool):
                return _decision_from_dict(prior)
            return FlagDecision(key, fallback, prior["config_version"], "type_mismatch", "fallback")

        inherited = request.inherited.get(key)
        if inherited is not None:
            wire, origin = inherited
            if isinstance(wire["value"], bool):
                decision = _decision_from_dict({**wire, "source": "inherited", "inherited_from": dict(origin)})
            else:
                version = request.bundle["version"] if request.bundle is not None else wire["config_version"]
                decision = FlagDecision(key, fallback, version, "type_mismatch", "fallback")
        else:
            decision = (
                evaluate_flag(request.bundle, key, request.customer, fallback)
                if request.fresh and request.bundle is not None
                else FlagDecision(
                    key,
                    fallback,
                    request.bundle["version"] if request.bundle is not None else 0,
                    "configuration_stale",
                    "fallback",
                )
            )
        self._record_decision(request, decision)
        return decision

    def variant(self, key: str, fallback: str) -> FlagDecision:
        """Evaluate a named variant inside run_request."""
        request = self._current_request()
        prior = request.evidence.get(key)
        if prior is not None:
            if isinstance(prior["value"], str):
                return _decision_from_dict(prior, variant=True)
            return FlagDecision(
                key,
                fallback,
                prior["config_version"],
                "type_mismatch",
                "fallback",
                type_="variant",
            )

        inherited = request.inherited.get(key)
        if inherited is not None:
            wire, origin = inherited
            if isinstance(wire["value"], str):
                decision = _decision_from_dict(
                    {**wire, "type": "variant", "source": "inherited", "inherited_from": dict(origin)},
                    variant=True,
                )
            else:
                version = request.bundle["version"] if request.bundle is not None else wire["config_version"]
                decision = FlagDecision(key, fallback, version, "type_mismatch", "fallback", type_="variant")
        else:
            decision = (
                evaluate_variant(request.bundle, key, request.customer, fallback)
                if request.fresh and request.bundle is not None
                else FlagDecision(
                    key,
                    fallback,
                    request.bundle["version"] if request.bundle is not None else 0,
                    "configuration_stale",
                    "fallback",
                    type_="variant",
                )
            )
        self._record_decision(request, decision)
        return decision

    def _current_request(self) -> _RequestState:
        request = self._request.get()
        if request is None:
            raise RuntimeError("Flag checks require run_request")
        return request

    @staticmethod
    def _record_decision(request: _RequestState, decision: FlagDecision) -> None:
        if len(request.evidence) < _MAX_EVIDENCE:
            request.evidence[decision.flag] = {**decision.to_dict(), "used": False}

    def used(self, key: str) -> None:
        """Mark a flag when the selected application behavior is entered."""
        request = self._request.get()
        evidence = request.evidence.get(key) if request is not None else None
        if evidence is None and request is not None and len(request.evidence) >= _MAX_EVIDENCE:
            return
        if evidence is None:
            raise RuntimeError("Flag must be evaluated before marking exposure")
        evidence["used"] = True

    def evidence(self) -> list[dict[str, Any]]:
        """Return a copy of this request's bounded evaluation evidence."""
        request = self._request.get()
        if request is None:
            return []
        return [dict(row) for row in request.evidence.values()]

    def response_evidence(self) -> str:
        """Encode request evidence for the gateway-consumed response header."""
        raw = json.dumps(self.evidence(), separators=(",", ":"), ensure_ascii=False).encode()
        return _b64url_encode(raw)

    def propagation_header(self) -> str | None:
        """Encode only used decisions for a managed call or explicitly scoped work."""
        request = self._request.get()
        if request is None or request.customer is None:
            return None
        app_id = os.environ.get("FAAS_APP_ID", "")
        environment_id = str(request.bundle["environment_id"]) if request.bundle is not None else ""
        decisions: list[dict[str, Any]] = []
        for evidence in request.evidence.values():
            if not evidence["used"]:
                continue
            inherited_origin = evidence.get("inherited_from")
            if isinstance(inherited_origin, dict):
                origin = inherited_origin
            elif _UUID.fullmatch(app_id) and _UUID.fullmatch(environment_id):
                origin = {"app_id": app_id.lower(), "environment_id": environment_id.lower()}
            else:
                continue
            source = evidence["source"]
            if source == "inherited":
                source = "fallback" if evidence["reason"] in _FALLBACK_REASONS else "configuration"
            decision = {
                key: value for key, value in evidence.items() if key not in {"used", "source", "inherited_from"}
            }
            decisions.append({**decision, "source": source, "origin": origin})
        if not decisions:
            return None
        decisions.sort(key=lambda row: row["flag"])
        raw = json.dumps(
            {"version": 1, "customer_id": request.customer, "decisions": decisions},
            separators=(",", ":"),
            ensure_ascii=False,
        ).encode()
        encoded = _b64url_encode(raw)
        if len(raw) > _MAX_PROPAGATION_BYTES or len(encoded) > _MAX_PROPAGATION_HEADER_BYTES:
            return None
        return encoded


def _decision_from_dict(row: Mapping[str, Any], *, variant: bool = False) -> FlagDecision:
    inherited_from = row.get("inherited_from")
    return FlagDecision(
        flag=row["flag"],
        value=row["value"],
        config_version=row["config_version"],
        reason=row["reason"],
        source=row["source"],
        type_="variant" if variant or row.get("type") == "variant" else None,
        rule_id=row.get("rule_id"),
        bucket=row.get("bucket"),
        rollout_bucket=row.get("rollout_bucket"),
        inherited_from=dict(inherited_from) if isinstance(inherited_from, dict) else None,
    )


class GregaleFlagsMiddleware:
    """ASGI middleware that scopes flag evaluation and emits response evidence."""

    def __init__(self, app: Any, flags: GregaleFlags) -> None:
        self.app = app
        self.flags = flags

    async def __call__(self, scope: dict[str, Any], receive: Any, send: Any) -> None:
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return
        async with self.flags.run_request(scope.get("headers", ())):

            async def send_with_evidence(message: dict[str, Any]) -> None:
                if message.get("type") == "http.response.start":
                    header_name = GREGALE_FLAG_EVIDENCE_HEADER.lower().encode()
                    headers = [
                        (name, value) for name, value in message.get("headers", []) if name.lower() != header_name
                    ]
                    if self.flags.evidence():
                        headers.append((header_name, self.flags.response_evidence().encode()))
                    message = {**message, "headers": headers}
                await send(message)

            await self.app(scope, receive, send_with_evidence)


def _is_managed_service(request: httpx.Request) -> bool:
    return request.url.host.lower().rstrip(".").endswith(".svc.gregale")


class AsyncGregaleFlagsTransport(httpx.AsyncBaseTransport):
    """HTTPX transport that forwards used flag decisions only to Gregale services."""

    def __init__(
        self,
        flags: GregaleFlags,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self.flags = flags
        self._transport = transport or httpx.AsyncHTTPTransport()

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        request.headers.pop(GREGALE_FLAG_PROPAGATION_HEADER, None)
        if _is_managed_service(request):
            flag_context = self.flags.propagation_header()
            if flag_context:
                request.headers[GREGALE_FLAG_PROPAGATION_HEADER] = flag_context
        return await self._transport.handle_async_request(request)

    async def aclose(self) -> None:
        await self._transport.aclose()


__all__ = [
    "GREGALE_FLAG_CONTEXT_HEADER",
    "GREGALE_FLAG_EVIDENCE_HEADER",
    "GREGALE_FLAG_PROPAGATION_HEADER",
    "AsyncGregaleFlagsTransport",
    "FlagDecision",
    "GregaleFlags",
    "GregaleFlagsMiddleware",
    "evaluate_flag",
    "evaluate_variant",
    "flag_bucket",
    "flag_variant_bucket",
    "validate_bundle",
]
