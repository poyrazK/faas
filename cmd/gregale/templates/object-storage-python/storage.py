"""Private file service over the canonical async binding and reload lifecycle."""

import asyncio
import hashlib
import hmac
import logging
import os
import tempfile
from contextlib import AsyncExitStack
from dataclasses import dataclass

from aiobotocore.config import AioConfig
from aiobotocore.session import get_session
from runtime.async_object_storage_runtime import AsyncObjectStorageBinding
from runtime.async_secret_reload_runtime import (
    AsyncSecretConsumer,
    AsyncSecretReloadCancellation,
    AsyncSecretReloadCoordinator,
)
from starlette.responses import JSONResponse, Response

logger = logging.getLogger(__name__)
# Sample application safeguards, independent of Gregale account/bucket quotas.
UPLOAD_BYTES = 10 * 1024 * 1024
UPLOADS = 2
TRANSFERS = 16


class StorageError(Exception):
    """Stable application error; never include driver or credential diagnostics."""

    def __init__(self, code="storage_unavailable", status=503):
        super().__init__(code)
        self.code, self.status = code, status


@dataclass(repr=False)
class OwnedS3:
    client: object
    stack: AsyncExitStack


@dataclass(eq=False)
class _Work:
    task: asyncio.Task
    cancel: asyncio.Event


def _consume(task):
    if not task.cancelled():
        task.exception()


def _tls_verify(environment):
    # Pass trust explicitly: verify=None also reads ambient AWS profile config,
    # while False/empty values can disable verification in the driver.
    if "AWS_CA_BUNDLE" not in environment:
        return True
    path = environment["AWS_CA_BUNDLE"]
    if not isinstance(path, str) or not path.strip() or any(char in path for char in "\r\n\0"):
        raise StorageError()
    return path


class Storage:
    def __init__(self, *, env=None, io=None, client_factory=None, spool_root=None,
                 transfer_timeout=30, restore_timeout=1.5, shutdown_timeout=5):
        environment = dict(os.environ if env is None else env)
        self._spool_root = spool_root
        self._transfer_timeout = transfer_timeout
        self._restore_timeout = min(restore_timeout, 1.5)
        self._shutdown_timeout = shutdown_timeout
        self._active = set()
        self._uploads = 0
        self._token = None
        self._started = self._closed = self._restore_failed = False
        self._starting = self._restoring = self._closing = None
        self._restore_cancel = None
        self.generation = 0

        async def factory(config, cancel):
            if client_factory is not None:
                return await client_factory(config, cancel)
            stack = AsyncExitStack()
            try:
                cancel.check()
                client = await stack.enter_async_context(get_session().create_client(
                    "s3", endpoint_url=config.endpoint, region_name=config.region,
                    aws_access_key_id=config.access_key_id, aws_secret_access_key=config.secret_access_key,
                    aws_session_token=None, verify=_tls_verify(environment),
                    config=AioConfig(signature_version="s3v4", s3={"addressing_style": config.addressing_style},
                                     request_checksum_calculation=config.request_checksum_calculation,
                                     response_checksum_validation=config.response_checksum_validation,
                                     retries={"total_max_attempts": 1}, connect_timeout=3, read_timeout=3,
                                     max_pool_connections=5, proxies={}),
                ))
                cancel.check()
                return OwnedS3(client, stack)
            except BaseException:
                await stack.aclose()  # Partial allocations belong to this factory.
                raise

        async def validate(owned, bucket, cancel):
            cancel.check()
            async with asyncio.timeout(3):
                await owned.client.list_objects_v2(Bucket=bucket, MaxKeys=1)
            cancel.check()

        async def retire(owned):
            await owned.stack.aclose()

        self.binding = AsyncObjectStorageBinding("GREGALE_S3_ASSETS", factory, validate=validate, retire=retire,
                                                on_retire_error=lambda _error: logger.warning("storage_retirement_failed"))

        async def authorize(snapshot, cancel):
            cancel.check()
            value = snapshot.secrets.get("STORAGE_APP_TOKEN", "")
            if not 32 <= len(value) <= 512 or any(char in value for char in "\r\n\0"):
                raise StorageError("invalid_app_token")
            self._token = hashlib.sha256(value.encode()).digest()

        def invalidate_auth():
            self._token = None

        self.coordinator = AsyncSecretReloadCoordinator(
            [self.binding.secret_consumer(), AsyncSecretConsumer(authorize, invalidate_auth)],
            env=environment, io=io,
            on_error=lambda _error: logger.warning("storage_adoption_failed"),
        )

    async def start(self):
        if self._closed:
            raise StorageError()
        if self._starting is None:
            self._starting = asyncio.create_task(self.coordinator.start())
            self._starting.add_done_callback(_consume)
        try:
            await asyncio.shield(self._starting)
            if self._closed:
                raise StorageError()
            self._started = True
        except asyncio.CancelledError:
            self.coordinator.stop()
            raise
        except Exception:
            raise StorageError() from None

    def ready(self):
        if not self._started or self._closed or self._restore_failed or self._restoring is not None or self._token is None:
            return False
        try:
            lease = self.binding.acquire()
            lease.release()
            return True
        except Exception:
            return False

    def authorize(self, header):
        if not self.ready():
            raise StorageError()
        if (not header.startswith("Bearer ") or len(header) > 1024
                or not hmac.compare_digest(hashlib.sha256(header[7:].encode()).digest(), self._token)):
            raise StorageError("unauthorized", 401)

    @staticmethod
    def _cancel(work):
        work.cancel.set()
        if not work.task.done() and not work.task.cancelling():
            work.task.cancel()

    def admit(self, response, scope, receive, send, body_received):
        self.authorize(response.request.headers.get("authorization", ""))
        upload = response.request.method == "PUT"
        if len(self._active) >= TRANSFERS or upload and self._uploads >= UPLOADS:
            raise StorageError("transfer_capacity")
        lease = self.binding.acquire()
        cancel = asyncio.Event()
        task = asyncio.create_task(response.serve(scope, receive, send, lease, cancel, body_received))
        work = _Work(task, cancel)
        self._active.add(work)
        self._uploads += int(upload)

        def finished(done):
            # Also runs if cancelled before its first step. A late driver result
            # keeps the task, body/spool and lease owned until actual completion.
            lease.release()
            self._active.discard(work)
            self._uploads -= int(upload)
            _consume(done)

        task.add_done_callback(finished)
        return work

    async def restore(self):
        if not self._started or self._closed:
            raise StorageError()
        if self._restoring is not None and self._restoring.done():
            self._restoring = None
        owner = self._restoring is None
        if owner:
            self._restore_failed = True
            self._token = None
            for work in tuple(self._active):
                self._cancel(work)
            self.binding.invalidate()
            self._restore_cancel = asyncio.Event()
            self._restoring = asyncio.create_task(self._recover(self._restore_cancel))
            self._restoring.add_done_callback(_consume)
            self._restoring.add_done_callback(self._restore_finished)
        task = self._restoring
        try:
            await asyncio.shield(task)
        except asyncio.CancelledError:
            if owner and not task.done():
                self._restore_cancel.set()
                self.binding.invalidate()
                task.cancel()
                await asyncio.gather(asyncio.shield(task), return_exceptions=True)
            raise
        finally:
            if task.done() and self._restoring is task:
                self._restoring = None

    def _restore_finished(self, task):
        if self._restoring is task:
            self._restoring = None

    async def _recover(self, cancel):
        try:
            async with asyncio.timeout(self._restore_timeout):
                # Always reread/rebuild, including at an unchanged revision.
                await self.coordinator.reload(timeout=self._restore_timeout, cancel=cancel)
                await self.binding.wait_for_retirement()
                lease = self.binding.acquire()
                lease.release()
                if cancel.is_set() or self._closed or self._token is None:
                    raise StorageError()
                self.generation += 1
                self._restore_failed = False
        except BaseException:
            self.binding.invalidate()
            logger.warning("storage_restore_failed")
            raise StorageError() from None

    def stop(self):
        """Close admission/reloads synchronously when the server gets a signal."""
        if self._closing is None:
            self._closed = True
            self.coordinator.stop()
            if self._restore_cancel is not None:
                self._restore_cancel.set()
            self._closing = asyncio.create_task(self._drain())
            self._closing.add_done_callback(_consume)

    async def close(self):
        self.stop()
        await asyncio.shield(self._closing)

    async def _drain(self):
        try:
            async with asyncio.timeout(self._shutdown_timeout):
                await self.coordinator.wait_stopped()
                if self._restoring is not None:
                    await asyncio.gather(asyncio.shield(self._restoring), return_exceptions=True)
                await self.binding.wait_for_retirement()
        except BaseException:
            for work in tuple(self._active):
                self._cancel(work)
            raise StorageError("storage_cleanup_incomplete") from None


class TransferResponse(Response):
    """Own transfer work independently of HTTP caller cancellation."""

    def __init__(self, service, request, key):
        super().__init__()
        self.service, self.request, self.key = service, request, key
        self.started = False

    async def __call__(self, scope, receive, send):
        body_received = asyncio.Event()
        if self.request.method == "GET":
            body_received.set()
        try:
            work = self.service.admit(self, scope, receive, send, body_received)
        except StorageError as error:
            await JSONResponse({"code": error.code}, status_code=error.status)(scope, receive, send)
            return

        async def disconnected():
            # The upload owns receive until its body is fully staged. Only then
            # can a separate watcher observe disconnects during the provider PUT.
            await body_received.wait()
            while (await receive())["type"] != "http.disconnect":
                pass

        watcher = asyncio.create_task(disconnected())
        deadline = asyncio.create_task(asyncio.sleep(self.service._transfer_timeout))
        try:
            await asyncio.wait((work.task, watcher, deadline), return_when=asyncio.FIRST_COMPLETED)
            if work.task.done() and not work.task.cancelled():
                work.task.result()
            elif not watcher.done():
                self.service._cancel(work)
                if self.started:
                    raise StorageError("storage_stream_failed")
                await JSONResponse({"code": "storage_unavailable"}, status_code=503)(scope, receive, send)
        finally:
            if not work.task.done():
                self.service._cancel(work)
            watcher.cancel()
            deadline.cancel()
            await asyncio.gather(watcher, deadline, return_exceptions=True)

    async def serve(self, scope, receive, send, lease, cancel, body_received):
        started = False
        token = AsyncSecretReloadCancellation([cancel]).with_timeout(self.service._transfer_timeout)

        async def emit(message):
            nonlocal started
            token.check()
            if message["type"] == "http.response.start":
                started = True
                self.started = True
            await send(message)

        try:
            async with asyncio.timeout(self.service._transfer_timeout):
                owned, bucket = lease.current()
                token.check()
                if self.request.method == "PUT":
                    await self.upload(owned.client, bucket, token, body_received)
                    await JSONResponse({"ok": True, "key": self.key}, status_code=201)(scope, receive, emit)
                else:
                    await self.download(owned.client, bucket, token, emit)
        except asyncio.CancelledError:
            raise asyncio.CancelledError from None
        except Exception as error:
            token.check()
            if started:
                raise StorageError("storage_stream_failed") from None
            if isinstance(error, StorageError):
                status, code = error.status, error.code
            elif getattr(error, "response", {}).get("Error", {}).get("Code") == "NoSuchKey":
                status, code = 404, "not_found"
            else:
                status, code = 503, "storage_unavailable"
            await JSONResponse({"code": code}, status_code=status, headers={"cache-control": "no-store"})(scope, receive, emit)

    async def upload(self, client, bucket, token, body_received):
        declared = self.request.headers.get("content-length")
        if declared is not None and (not declared.isascii() or not declared.isdecimal() or len(declared) > 10
                                     or int(declared) > UPLOAD_BYTES):
            raise StorageError("upload_too_large", 413)
        with tempfile.TemporaryDirectory(prefix="gregale-upload-", dir=self.service._spool_root) as directory:
            # TemporaryDirectory is 0700; the file is created 0600 independently
            # of process umask. No unbounded in-memory upload accumulation.
            descriptor = os.open(os.path.join(directory, "body"), os.O_CREAT | os.O_EXCL | os.O_RDWR, 0o600)
            with os.fdopen(descriptor, "w+b") as spool:
                size = 0
                async for chunk in self.request.stream():
                    token.check()
                    size += len(chunk)
                    if size > UPLOAD_BYTES:
                        raise StorageError("upload_too_large", 413)
                    spool.write(chunk)
                if declared is not None and size != int(declared):
                    raise StorageError("invalid_upload_size", 400)
                spool.seek(0)
                body_received.set()
                token.check()
                await client.put_object(Bucket=bucket, Key=self.key, Body=spool, ContentLength=size,
                                        ContentType="application/octet-stream")
                token.check()  # An uncertain PUT is never replayed.

    async def download(self, client, bucket, token, send):
        result = await client.get_object(Bucket=bucket, Key=self.key)
        body = result["Body"]
        try:
            token.check()  # Close a late response before releasing its lease.
            length = int(result["ContentLength"])
            if length < 0:
                raise StorageError()
            await send({"type": "http.response.start", "status": 200, "headers": [
                (b"content-type", b"application/octet-stream"), (b"content-disposition", b"attachment"),
                (b"x-content-type-options", b"nosniff"), (b"cache-control", b"no-store"),
                (b"content-length", str(length).encode()),
            ]})
            size = 0
            while chunk := await body.read(65536):
                token.check()
                size += len(chunk)
                await send({"type": "http.response.body", "body": chunk, "more_body": True})
            if size != length:
                raise StorageError("storage_stream_failed")
            await send({"type": "http.response.body", "body": b"", "more_body": False})
        finally:
            body.close()  # Pinned aiobotocore closes the raw HTTP response synchronously.
