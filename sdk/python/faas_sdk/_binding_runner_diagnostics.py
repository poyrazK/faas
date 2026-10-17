"""Bounded, read-only recovery details for the original verification receipt."""

import asyncio
import copy
import threading
import time
from datetime import datetime
from uuid import UUID

from attrs import fields, has

from .models.binding_verification_run_diagnostics import BindingVerificationRunDiagnostics
from .models.project_binding_verification_run_diagnostics import ProjectBindingVerificationRunDiagnostics

DIAGNOSTICS_TIMEOUT = 2.0
Diagnostics = BindingVerificationRunDiagnostics | ProjectBindingVerificationRunDiagnostics


def _date(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def _clear_unknown(value):
    if has(type(value)):
        value.additional_properties.clear()
        for field in fields(type(value)):
            if field.name != "additional_properties":
                _clear_unknown(getattr(value, field.name))
    elif isinstance(value, dict):
        for child in value.values():
            _clear_unknown(child)
    elif isinstance(value, list):
        for child in value:
            _clear_unknown(child)


def _app_identity(raw, app, run_id, original=None):
    if raw["run_id"] != run_id or raw["app"] != app or not raw["scope"] or UUID(raw["deployment_id"]).int == 0:
        raise ValueError("unconfirmed diagnostics")
    _date(raw["checked_at"])
    _date(raw["expires_at"])
    if original and (
        UUID(raw["deployment_id"]) != UUID(original["deployment_id"])
        or raw["scope"] != original["scope"]
        or raw.get("graph_run_id", "") != original.get("graph_run_id", "")
        or _date(raw["expires_at"]) != _date(original["expires_at"])
    ):
        raise ValueError("changed diagnostic selection")


def _decode(raw, identity, original):
    if "app" in identity:
        _app_identity(raw, identity["app"], identity["id"], original)
        out = BindingVerificationRunDiagnostics.from_dict(raw)
    else:
        if any(
            raw[key] != identity[source]
            for key, source in (("run_id", "id"), ("project", "project"), ("environment", "environment"))
        ):
            raise ValueError("unconfirmed graph diagnostics")
        _date(raw["checked_at"])
        expiry = _date(raw["expires_at"])
        if original and (expiry != _date(original["expires_at"]) or set(raw["members"]) != set(original["members"])):
            raise ValueError("changed diagnostic graph")
        for name, member in raw["members"].items():
            expected = original["members"][name] if original else None
            if (
                member["graph_run_id"] != identity["id"]
                or member["scope"] != identity["environment"]
                or _date(member["expires_at"]) != expiry
            ):
                raise ValueError("changed diagnostic member")
            _app_identity(member, name, expected["id"] if expected else member["run_id"], expected)
        out = ProjectBindingVerificationRunDiagnostics.from_dict(raw)
    _clear_unknown(out)
    return out


def _budget(identity, original, stop_event):
    if stop_event is not None and stop_event.is_set():
        return 0
    try:
        if UUID(identity["id"]).int == 0 or str(UUID(identity["id"])) != identity["id"]:
            return 0
        return (
            min(DIAGNOSTICS_TIMEOUT, _date(original["expires_at"]) - time.time()) if original else DIAGNOSTICS_TIMEOUT
        )
    except (ValueError, TypeError, AttributeError):
        return 0


def _timeout(transport, remaining):
    timeout = copy.copy(transport.timeout)
    for name in ("connect", "read", "write", "pool"):
        configured = getattr(timeout, name)
        setattr(timeout, name, min(configured, remaining) if configured is not None else remaining)
    return timeout


def diagnose_sync(client, identity, path, original, stop_event) -> Diagnostics | None:
    remaining = _budget(identity, original, stop_event)
    if remaining <= 0:
        return None
    # A daemon observer bounds total latency (not just each HTTP phase) and
    # lets a synchronous stop_event return immediately during a blocked read.
    # It performs only one GET and never invokes user callbacks or changes work.
    finished, result = threading.Event(), [None]
    transport = client.get_httpx_client()
    timeout = _timeout(transport, remaining)

    def read():
        try:
            response = transport.get(path + "/diagnostics", timeout=timeout)
            if response.status_code == 200:
                result[0] = _decode(response.json(), identity, original)
        except Exception:
            pass
        finally:
            finished.set()

    threading.Thread(target=read, name="gregale-binding-diagnostics", daemon=True).start()
    until = time.monotonic() + remaining
    while not finished.is_set():
        if stop_event is not None and stop_event.is_set():
            return None
        left = until - time.monotonic()
        if left <= 0:
            return None
        finished.wait(min(left, 0.01))
    if stop_event is not None and stop_event.is_set():
        return None
    return result[0]


async def diagnose_async(client, identity, path, original, stop_event) -> Diagnostics | None:
    remaining = _budget(identity, original, stop_event)
    if remaining <= 0:
        return None
    transport = client.get_async_httpx_client()
    request = asyncio.create_task(transport.get(path + "/diagnostics", timeout=_timeout(transport, remaining)))
    stopper = asyncio.create_task(stop_event.wait()) if stop_event is not None else None
    try:
        done, _ = await asyncio.wait(
            [request, *([stopper] if stopper else [])], timeout=remaining, return_when=asyncio.FIRST_COMPLETED
        )
        if request not in done or stopper is not None and stopper in done:
            return None
        response = await request
        if response.status_code == 200:
            return _decode(response.json(), identity, original)
    except Exception:
        return None
    finally:
        request.cancel()
        if stopper:
            stopper.cancel()
        await asyncio.gather(request, *([stopper] if stopper else []), return_exceptions=True)
    return None
