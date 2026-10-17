"""Disposable HTTP S3 fixture that verifies SigV4 and can hold response bodies."""

import asyncio
import hashlib
import hmac
import importlib.util
import json
import os
import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

from botocore.auth import S3SigV4Auth
from botocore.awsrequest import AWSRequest
from botocore.credentials import Credentials


class SignedS3:
    first = ("access-first", "private-first-secret")
    second = ("access-second", "private-second-secret")
    region = "eu-test-1"
    data = b"a" * 65536 + b"b" * 65536

    def __init__(self):
        self.keys = dict([self.first])
        self.requests, self.acks = [], []
        self.tasks, self.writers = set(), set()
        self.release = asyncio.Event()
        self.head_release = asyncio.Event()
        self.head_entered = asyncio.Event()
        self.stream_started = asyncio.Event()
        self.stream_closed = asyncio.Event()
        self.hold_head = False

    async def __aenter__(self):
        self.server = await asyncio.start_server(self.handle, "127.0.0.1", 0)
        self.endpoint = f"http://127.0.0.1:{self.server.sockets[0].getsockname()[1]}"
        return self

    async def __aexit__(self, *_args):
        self.server.close()
        await self.server.wait_closed()
        self.release.set()
        self.head_release.set()
        for writer in tuple(self.writers):
            writer.close()
        async with asyncio.timeout(3):
            await asyncio.gather(*self.tasks, return_exceptions=True)

    def values(self, credentials=None):
        access, secret = credentials or self.first
        return {
            "FILES_ENDPOINT": self.endpoint,
            "FILES_REGION": self.region,
            "FILES_BUCKET": "assets",
            "FILES_ACCESS_KEY_ID": access,
            "FILES_SECRET_ACCESS_KEY": secret,
            "FILES_ADDRESSING_STYLE": "path",
        }

    def authenticate(self, method, target, headers, body):
        match = re.fullmatch(
            r"AWS4-HMAC-SHA256 Credential=([^/]+)/([^,]+), SignedHeaders=([^,]+), Signature=([a-f0-9]{64})",
            headers.get("authorization", ""),
        )
        if not match or "x-amz-security-token" in headers:
            return None
        access, scope, signed, signature = match.groups()
        if access not in self.keys or not scope.endswith(f"/{self.region}/s3/aws4_request"):
            return None
        selected = {name: headers[name] for name in signed.split(";")}
        request = AWSRequest(method=method, url=self.endpoint + target, headers=selected, data=body)
        request.context["timestamp"] = headers["x-amz-date"]
        signer = S3SigV4Auth(Credentials(access, self.keys[access]), "s3", self.region)
        canonical = signer.canonical_request(request)
        expected = signer.signature(signer.string_to_sign(request, canonical), request)
        return access if hmac.compare_digest(signature, expected) else None

    @staticmethod
    async def gate(reader, event):
        ready, closed = asyncio.create_task(event.wait()), asyncio.create_task(reader.read())
        try:
            await asyncio.wait((ready, closed), return_when=asyncio.FIRST_COMPLETED)
            return ready.done()
        finally:
            ready.cancel()
            closed.cancel()
            await asyncio.gather(ready, closed, return_exceptions=True)

    @staticmethod
    async def reply(writer, status, body=b"", *, head=False):
        writer.write(f"HTTP/1.1 {status} Fixture\r\nContent-Length: {len(body)}\r\nConnection: close\r\n\r\n".encode())
        if not head:
            writer.write(body)
        await writer.drain()

    async def handle(self, reader, writer):
        task = asyncio.current_task()
        self.tasks.add(task)
        self.writers.add(writer)
        slow = False
        try:
            async with asyncio.timeout(20):
                lines = (await reader.readuntil(b"\r\n\r\n")).decode().split("\r\n")
                method, target, _version = lines[0].split(" ")
                headers = {
                    key.lower(): value.strip() for key, value in (line.split(":", 1) for line in lines[1:] if line)
                }
                body = await reader.readexactly(int(headers.get("content-length", "0")))
                if method == "POST" and target == "/ack":
                    self.acks.append(json.loads(body))
                    await self.reply(writer, 202)
                    return
                access = self.authenticate(method, target, headers, body)
                path = unquote(urlsplit(target).path)
                self.requests.append((method, path, access))
                if access is None:
                    await self.reply(writer, 403, b"<Error><Code>AccessDenied</Code></Error>", head=method == "HEAD")
                elif method == "HEAD" and path == "/assets/readiness.txt":
                    self.head_entered.set()
                    if self.hold_head and not await self.gate(reader, self.head_release):
                        return
                    await self.reply(writer, 200, b"ready", head=True)
                elif method == "GET" and path == "/assets/slow.bin":
                    slow = True
                    writer.write(
                        f"HTTP/1.1 200 OK\r\nContent-Length: {len(self.data)}\r\nConnection: close\r\n\r\n".encode()
                    )
                    writer.write(self.data[:65536])
                    await writer.drain()
                    self.stream_started.set()
                    if await self.gate(reader, self.release):
                        writer.write(self.data[65536:])
                        await writer.drain()
                elif method == "GET" and path == "/assets/hello.txt":
                    await self.reply(writer, 200, b"hello")
                elif method == "GET" and path == "/assets/broken.bin":
                    writer.write(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\nshort")
                    await writer.drain()  # Deliberate truncation, never replayed.
                else:
                    await self.reply(writer, 404, b"<Error><Code>NoSuchKey</Code></Error>", head=method == "HEAD")
        except (ConnectionError, asyncio.IncompleteReadError):
            pass  # Disconnects are intentional fixture events.
        finally:
            writer.close()
            try:
                await writer.wait_closed()
            except ConnectionError:
                pass
            if slow:
                self.stream_closed.set()
            self.tasks.discard(task)
            self.writers.discard(writer)


def load_example(monkeypatch):
    root = Path(__file__).resolve().parents[3] / "examples/object-storage-rotation-python"
    modules = []
    for name in ("storage", "object_storage_example_app"):
        source = root / ("app.py" if name.endswith("app") else "storage.py")
        spec = importlib.util.spec_from_file_location(name, source)
        module = importlib.util.module_from_spec(spec)
        monkeypatch.setitem(sys.modules, name, module)
        spec.loader.exec_module(module)
        modules.append(module)
    return modules


def projection(root, server, values):
    env = {
        "FAAS_SECRETS_SNAPSHOT_FILE": str(root / "secrets.json"),
        "FAAS_SECRETS_RELOAD_READY_FILE": str(root / "ready"),
        "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": server.endpoint + "/ack",
        "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32,
    }

    def publish(revision, secrets):
        file = root / "next.json"
        file.write_text(json.dumps({"revision": revision * 64, "secrets": secrets}))
        file.chmod(0o600)
        os.replace(file, env["FAAS_SECRETS_SNAPSHOT_FILE"])

    publish("a", values)
    return env, publish


def http_scope(path="/objects/slow.bin"):
    return {
        "type": "http",
        "asgi": {"version": "3.0"},
        "http_version": "1.1",
        "method": "GET",
        "scheme": "http",
        "path": path,
        "raw_path": path.encode(),
        "query_string": b"",
        "root_path": "",
        "headers": [],
        "client": ("198.51.100.2", 1234),
        "server": ("example", 80),
    }


async def open_stream(application):
    incoming, outgoing = asyncio.Queue(), asyncio.Queue()
    await incoming.put({"type": "http.request", "body": b"", "more_body": False})
    task = asyncio.create_task(application(http_scope(), incoming.get, outgoing.put))
    async with asyncio.timeout(5):
        start = await outgoing.get()
        assert start["status"] == 200
        chunk = await outgoing.get()
        assert chunk["type"] == "http.response.body" and chunk["body"]
    return task, incoming, outgoing, chunk["body"]


async def finish_stream(task, outgoing, initial):
    result = bytearray(initial)
    async with asyncio.timeout(5):
        while True:
            message = await outgoing.get()
            result.extend(message["body"])
            if not message.get("more_body", False):
                break
        await task
    return hashlib.sha256(result).digest()
