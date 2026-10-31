"""App-owned aiobotocore hooks and cancellation-safe streaming ownership."""

import asyncio
from contextlib import AsyncExitStack
from dataclasses import dataclass

from aiobotocore.config import AioConfig
from aiobotocore.session import get_session
from faas_sdk.preview.async_object_storage_runtime import AsyncObjectStorageBinding
from faas_sdk.async_secret_reload_runtime import AsyncSecretReloadCancellation
from starlette.responses import Response


@dataclass(repr=False)
class OwnedS3:
    client: object
    stack: AsyncExitStack

    def __repr__(self):
        return "OwnedS3(redacted)"


def make_binding(prefix="FILES", *, readiness_key="readiness.txt", on_retire_error=None):
    session = get_session()

    async def factory(config, cancel):
        stack = AsyncExitStack()
        try:
            cancel.check()
            client = await stack.enter_async_context(session.create_client(
                "s3", endpoint_url=config.endpoint, region_name=config.region,
                aws_access_key_id=config.access_key_id, aws_secret_access_key=config.secret_access_key,
                aws_session_token=None, verify=True,
                config=AioConfig(signature_version="s3v4", s3={"addressing_style": config.addressing_style},
                                 request_checksum_calculation=config.request_checksum_calculation,
                                 response_checksum_validation=config.response_checksum_validation,
                                 retries={"total_max_attempts": 1}, connect_timeout=3, read_timeout=3,
                                 max_pool_connections=5, proxies={}),
            ))
            cancel.check()
            return OwnedS3(client, stack)
        except BaseException:
            await stack.aclose()  # Factory owns partial allocations until return.
            raise

    async def validate(owned, bucket, cancel):
        cancel.check()
        async with asyncio.timeout(3):
            await owned.client.head_object(Bucket=bucket, Key=readiness_key)
        cancel.check()

    async def retire(owned):
        await owned.stack.aclose()

    return AsyncObjectStorageBinding(prefix, factory, validate=validate, retire=retire,
                                     on_retire_error=on_retire_error)


class StorageRequestError(Exception):
    """Stable stream failure without provider or credential diagnostics."""


class StorageService:
    """Retain owned request tasks until bodies really close, even after callers
    disconnect. The binding's lease then keeps its client alive during rotation.
    """

    def __init__(self, binding, *, download_timeout=15):
        self.binding = binding
        self.download_timeout = download_timeout
        self._operations = set()
        self._closed = False

    def download(self, key):
        return DownloadResponse(self, key)

    def stop(self):
        self._closed = True

    async def wait_closed(self):
        if self._operations:
            await asyncio.wait(tuple(self._operations))  # Cancelling this wait never cancels its children.

    def track(self, task):
        self._operations.add(task)
        task.add_done_callback(self._operations.discard)
        task.add_done_callback(lambda done: None if done.cancelled() else done.exception())


class DownloadResponse(Response):
    def __init__(self, service, key):
        super().__init__()
        self.service, self.key = service, key

    async def __call__(self, scope, receive, send):
        async def disconnected():
            while (await receive())["type"] != "http.disconnect":
                pass

        cancel = asyncio.Event()
        task = asyncio.create_task(self._serve(scope, receive, send, cancel))
        self.service.track(task)
        watcher = asyncio.create_task(disconnected())
        try:
            await asyncio.wait((task, watcher), return_when=asyncio.FIRST_COMPLETED)
            if task.done():
                task.result()
        finally:
            cancel.set()
            if not task.done() and not task.cancelling():
                task.cancel()
            watcher.cancel()
            await asyncio.gather(watcher, return_exceptions=True)

    async def _serve(self, scope, receive, send, cancel):
        started = False
        token = AsyncSecretReloadCancellation([cancel])
        try:
            if self.service._closed:
                raise StorageRequestError("Storage unavailable")
            async with asyncio.timeout(self.service.download_timeout):
                async with self.service.binding.acquire() as lease:
                    owned, bucket = lease.current()
                    response = await owned.client.get_object(Bucket=bucket, Key=self.key)
                    body = response["Body"]
                    try:
                        token.check()  # A late response after cancellation is closed, never forwarded.
                        length = int(response["ContentLength"])
                        if length < 0:
                            raise StorageRequestError("Invalid object length")
                        started = True
                        await send({"type": "http.response.start", "status": 200, "headers": [
                            (b"content-type", b"application/octet-stream"), (b"content-length", str(length).encode()),
                        ]})
                        while chunk := await body.read(65536):
                            token.check()
                            await send({"type": "http.response.body", "body": chunk, "more_body": True})
                        token.check()
                        await send({"type": "http.response.body", "body": b"", "more_body": False})
                    finally:
                        # aiobotocore's pinned StreamingBody.close() synchronously
                        # closes the underlying HTTP response, before lease exit.
                        body.close()
        except asyncio.CancelledError:
            raise asyncio.CancelledError from None
        except Exception:
            if started:
                raise StorageRequestError("Storage stream failed") from None
            await Response("Storage temporarily unavailable", status_code=503)(scope, receive, send)
