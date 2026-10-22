"""Pull-lease queue consumer for worker and job apps (ADR-1140).

A worker leases messages from a queue with an enabled pull binding, runs a
handler for each one, and settles it: ack on success, nack (retry under the
binding's policy) on an exception. While a handler runs, its lease is
extended at half the visibility timeout so slow work is not redelivered.
Inside a Gregale VM the consumer authenticates with a workload-identity token
for audience ``gregale:queues``; elsewhere it takes an API key with
``queues:consume``. A qualification candidate has neither: Gregale sets
``GREGALE_QUEUE_LEASE_URL`` to its node-local proxy, which serves only the
reviewed smoke message and needs no credential (ADR-20261022155812). That
base takes precedence over ``api_url`` and the credentials.

    from faas_sdk.queue_consumer import consume_queue

    async def handle(message):
        await process(message.payload)

    asyncio.run(consume_queue(app="my-worker", queue="jobs", handler=handle))
"""

from __future__ import annotations

import asyncio
import contextlib
import inspect
import ipaddress
import os
import signal
import time
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any
from urllib.parse import parse_qsl, quote, urlencode, urlsplit, urlunsplit

import httpx

DEFAULT_GREGALE_API_URL = "https://api.gregale.dev"
_QUEUES_AUDIENCE = "gregale:queues"
_MAX_BATCH = 100
_MAX_WAIT_SECONDS = 20
_ERROR_MAX_BYTES = 1024
_TOKEN_REFRESH_MARGIN_SECONDS = 60
_MAX_BACKOFF_SECONDS = 30.0
# A call may not outlive its server-side wait by more than this.
_CALL_SLACK_SECONDS = 10.0


@dataclass(frozen=True)
class QueueMessage:
    """One leased message. ``lease_lost`` is set when the lease is lost and the
    message may be redelivered to another consumer."""

    id: str
    payload: Any
    headers: Mapping[str, str]
    attempt: int
    enqueued_at: datetime
    lease_lost: asyncio.Event = field(repr=False, compare=False)


Handler = Callable[[QueueMessage], Any]


class QueueConsumer:
    """Leases and handles queue messages until stopped.

    Async handlers run on the event loop; plain functions run in a worker
    thread so lease extensions keep flowing while they block.
    """

    def __init__(
        self,
        *,
        app: str,
        queue: str,
        handler: Handler,
        api_url: str | None = None,
        api_key: str | None = None,
        identity_endpoint: str | None = None,
        concurrency: int = 1,
        visibility_timeout_seconds: int = 30,
        wait_seconds: int = _MAX_WAIT_SECONDS,
        retry_delay_seconds: int = 0,
        on_error: Callable[[BaseException], None] | None = None,
        async_client: httpx.AsyncClient | None = None,
    ) -> None:
        platform = os.environ.get("GREGALE_QUEUE_LEASE_URL")
        self._platform_lease = bool(platform)
        if platform:
            parts = _platform_lease_base(platform)
        else:
            base = api_url or os.environ.get("GREGALE_API_URL") or DEFAULT_GREGALE_API_URL
            parts = urlsplit(base)
            if parts.scheme != "https" or not parts.hostname or parts.username or parts.password:
                raise ValueError("Queue consumer requires an HTTPS API URL")
        if not app or not queue:
            raise ValueError("Queue consumer requires app and queue")
        for name, value, low, high in (
            ("concurrency", concurrency, 1, _MAX_BATCH),
            ("visibility_timeout_seconds", visibility_timeout_seconds, 2, None),
            ("wait_seconds", wait_seconds, 0, _MAX_WAIT_SECONDS),
            ("retry_delay_seconds", retry_delay_seconds, 0, None),
        ):
            in_range = value >= low and (high is None or value <= high) if isinstance(value, int) else False
            if isinstance(value, bool) or not in_range:
                raise ValueError(f"{name} is out of range")
        self._identity_url: str | None = None
        if not api_key and not self._platform_lease:
            endpoint = identity_endpoint or os.environ.get("FAAS_WORKLOAD_IDENTITY_ENDPOINT", "")
            identity = urlsplit(endpoint)
            if (
                identity.scheme != "http"
                or identity.hostname not in {"127.0.0.1", "::1", "localhost"}
                or identity.username
                or identity.password
            ):
                raise ValueError("Workload identity endpoint must be loopback HTTP; pass api_key outside a Gregale VM")
            query = [(k, v) for k, v in parse_qsl(identity.query) if k != "audience"]
            query.append(("audience", _QUEUES_AUDIENCE))
            self._identity_url = urlunsplit((identity.scheme, identity.netloc, identity.path, urlencode(query), ""))
        path = f"/v1/apps/{quote(app, safe='')}/queues/{quote(queue, safe='')}/leases"
        prefix = parts.path.rstrip("/") if self._platform_lease else ""
        self._leases_url = urlunsplit((parts.scheme, parts.netloc, prefix + path, "", ""))
        self._handler = handler
        self._api_key = api_key
        self._concurrency = concurrency
        self._visibility = visibility_timeout_seconds
        self._wait = wait_seconds
        self._retry_delay = retry_delay_seconds
        self._on_error = on_error
        self._client = async_client
        self._owns_client = async_client is None
        self._token: tuple[str, float] | None = None
        self._stopping = asyncio.Event()
        self._in_flight: set[asyncio.Task[None]] = set()

    async def run(self) -> None:
        """Run until :meth:`stop`; returns once in-flight messages are settled."""
        backoff = 0.0
        try:
            while not self._stopping.is_set():
                free = self._concurrency - len(self._in_flight)
                if free <= 0:
                    await self._wait_for(asyncio.wait(self._in_flight, return_when=asyncio.FIRST_COMPLETED))
                    continue
                try:
                    messages = await self._wait_for(self._lease(free))
                    backoff = 0.0
                except asyncio.CancelledError:
                    raise
                except Exception as error:  # reported, then retried
                    if self._stopping.is_set():
                        break
                    self._report(error)
                    backoff = min(max(backoff * 2, 1.0), _MAX_BACKOFF_SECONDS)
                    await self._wait_for(asyncio.sleep(backoff))
                    continue
                for message in messages or ():
                    task = asyncio.create_task(self._process(message))
                    self._in_flight.add(task)
                    task.add_done_callback(self._in_flight.discard)
            if self._in_flight:
                await asyncio.gather(*self._in_flight, return_exceptions=True)
        finally:
            if self._owns_client and self._client is not None:
                await self._client.aclose()
                self._client = None

    def stop(self) -> None:
        """Stop leasing; :meth:`run` returns after in-flight handlers finish and settle."""
        self._stopping.set()

    async def _wait_for(self, awaitable: Awaitable[Any]) -> Any:
        """Await ``awaitable`` unless stopping first; returns None when stopped.

        Stopping cancels an in-progress long-poll; a message leased in that
        instant is redelivered after its visibility timeout.
        """
        work = asyncio.ensure_future(awaitable)
        stop = asyncio.ensure_future(self._stopping.wait())
        try:
            await asyncio.wait({work, stop}, return_when=asyncio.FIRST_COMPLETED)
        finally:
            stop.cancel()
        if work.done():
            return work.result()
        work.cancel()
        with contextlib.suppress(asyncio.CancelledError, Exception):
            await work
        return None

    async def _lease(self, maximum: int) -> list[Mapping[str, Any]]:
        body = await self._call(
            "",
            {"max_messages": maximum, "visibility_timeout_seconds": self._visibility, "wait_seconds": self._wait},
            self._wait,
        )
        messages = body.get("messages") if isinstance(body, Mapping) else None
        if not isinstance(messages, list):
            raise ValueError("Invalid queue lease response")
        return messages

    async def _process(self, leased: Mapping[str, Any]) -> None:
        receipt = str(leased.get("receipt", ""))
        lost = asyncio.Event()
        message = QueueMessage(
            id=str(leased.get("id", "")),
            payload=leased.get("payload"),
            headers=dict(leased.get("headers") or {}),
            attempt=int(leased.get("attempt", 0)),
            enqueued_at=_parse_time(leased.get("enqueued_at")),
            lease_lost=lost,
        )
        heartbeat = asyncio.create_task(self._extend_until_done(receipt, lost))
        failure: BaseException | None = None
        try:
            if inspect.iscoroutinefunction(self._handler):
                result = self._handler(message)
            else:
                result = await asyncio.to_thread(self._handler, message)
            if inspect.isawaitable(result):
                await result
        except Exception as error:  # becomes a nack
            failure = error
        finally:
            heartbeat.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await heartbeat
        if failure is not None:
            self._report(failure)
        if lost.is_set():
            return  # the message belongs to its next delivery now
        try:
            if failure is None:
                held = await self._settle(":ack", receipt, {})
            else:
                held = await self._settle(
                    ":nack", receipt, {"error": _error_text(failure), "delay_seconds": self._retry_delay}
                )
            if not held:
                self._report(RuntimeError(f"Queue lease for message {message.id} expired before it was settled"))
        except Exception as error:  # redelivered after the timeout
            self._report(error)

    async def _extend_until_done(self, receipt: str, lost: asyncio.Event) -> None:
        while True:
            await asyncio.sleep(self._visibility / 2)
            try:
                if not await self._settle(":extend", receipt, {"visibility_timeout_seconds": self._visibility}):
                    lost.set()
                    return
            except asyncio.CancelledError:
                raise
            except Exception as error:  # the next tick retries
                self._report(error)

    async def _settle(self, verb: str, receipt: str, extra: Mapping[str, Any]) -> bool:
        out = await self._call(verb, {"receipts": [receipt], **extra}, 0)
        succeeded = out.get("succeeded") if isinstance(out, Mapping) else None
        return isinstance(succeeded, list) and receipt in succeeded

    async def _call(self, verb: str, body: Mapping[str, Any], wait_seconds: float) -> Any:
        client = self._http_client()
        headers = {} if self._platform_lease else {"Authorization": f"Bearer {await self._credential()}"}
        response = await client.post(
            self._leases_url + verb,
            json=body,
            headers=headers,
            timeout=wait_seconds + _CALL_SLACK_SECONDS,
        )
        if response.status_code == 401:
            self._token = None
        if not response.is_success:
            raise RuntimeError(f"Queue {verb.lstrip(':') or 'lease'} failed ({response.status_code})")
        return response.json()

    async def _credential(self) -> str:
        if self._api_key:
            return self._api_key
        if self._token is not None and time.monotonic() < self._token[1]:
            return self._token[0]
        assert self._identity_url is not None
        response = await self._http_client().get(self._identity_url, timeout=5.0)
        if not response.is_success:
            raise RuntimeError("Queue workload identity unavailable")
        token = response.json()
        access = token.get("access_token") if isinstance(token, Mapping) else None
        if not isinstance(access, str) or not access or len(access) > 8192:
            raise ValueError("Invalid workload identity")
        expires_in = token.get("expires_in")
        lifetime = float(expires_in) if isinstance(expires_in, (int, float)) and expires_in > 0 else 300.0
        self._token = (access, time.monotonic() + max(lifetime - _TOKEN_REFRESH_MARGIN_SECONDS, lifetime / 2))
        return access

    def _http_client(self) -> httpx.AsyncClient:
        if self._client is None:
            self._client = httpx.AsyncClient(follow_redirects=False, trust_env=False)
        return self._client

    def _report(self, error: BaseException) -> None:
        if self._on_error is None:
            return
        with contextlib.suppress(Exception):
            self._on_error(error)


async def consume_queue(*, handle_signals: bool = True, **options: Any) -> None:
    """Run a :class:`QueueConsumer` until SIGTERM or SIGINT (with ``handle_signals``),
    letting in-flight handlers finish within the app's stop grace period."""
    consumer = QueueConsumer(**options)
    if handle_signals:
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            with contextlib.suppress(NotImplementedError, RuntimeError):
                loop.add_signal_handler(sig, consumer.stop)
    try:
        await consumer.run()
    finally:
        if handle_signals:
            loop = asyncio.get_running_loop()
            for sig in (signal.SIGTERM, signal.SIGINT):
                with contextlib.suppress(NotImplementedError, RuntimeError):
                    loop.remove_signal_handler(sig)


def _error_text(error: BaseException) -> str:
    text = f"{type(error).__name__}: {error}"
    encoded = text.encode("utf-8")
    if len(encoded) <= _ERROR_MAX_BYTES:
        return text
    return encoded[:_ERROR_MAX_BYTES].decode("utf-8", errors="ignore")


def _parse_time(value: Any) -> datetime:
    if isinstance(value, str):
        with contextlib.suppress(ValueError):
            return datetime.fromisoformat(value.replace("Z", "+00:00"))
    return datetime.fromtimestamp(0).astimezone()


def _platform_lease_base(raw: str) -> Any:
    """The platform lease base is HTTPS, or plain HTTP to the node's private
    bridge address; a candidate's egress reaches nothing else."""
    parts = urlsplit(raw)
    if not parts.hostname or parts.username or parts.password or parts.query or parts.fragment:
        raise ValueError("Invalid GREGALE_QUEUE_LEASE_URL")
    if parts.scheme == "https":
        return parts
    try:
        address = ipaddress.ip_address(parts.hostname)
    except ValueError:
        address = None
    if parts.scheme == "http" and isinstance(address, ipaddress.IPv4Address) and address.is_private and not address.is_loopback:
        return parts
    raise ValueError("GREGALE_QUEUE_LEASE_URL must be HTTPS or a private IPv4 HTTP address")
