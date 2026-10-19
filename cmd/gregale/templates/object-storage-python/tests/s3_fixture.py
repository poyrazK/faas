"""Owned loopback S3 fixture: actual SigV4, exact bytes and controllable sockets."""

import asyncio
import hashlib
import hmac
import json
import os
import re
from urllib.parse import unquote, urlsplit

from botocore.auth import S3SigV4Auth
from botocore.awsrequest import AWSRequest
from botocore.credentials import Credentials

TOKEN = "first-private-application-token-12345678"
SECOND_TOKEN = "second-private-application-token-12345678"


class SignedS3:
    first = ("access-first", "private-first-secret")
    second = ("access-second", "private-second-secret")
    region = "eu-test-1"
    data = bytes(range(256)) * 512

    def __init__(self, *, ssl_context=None):
        self.ssl_context = ssl_context
        self.keys = dict([self.first])
        self.requests, self.acks, self.puts, self.tls_requests = [], [], [], []
        self.objects = {"hello.bin": b"hello\x00world"}
        self.tasks, self.writers = set(), set()
        self.stream_started, self.stream_closed, self.release = asyncio.Event(), asyncio.Event(), asyncio.Event()
        self.list_entered, self.list_release = asyncio.Event(), asyncio.Event()
        self.put_entered, self.put_release = asyncio.Event(), asyncio.Event()
        self.hold_list = self.hold_put = self.fail_list = False

    async def __aenter__(self):
        self.server = await asyncio.start_server(self.handle, "127.0.0.1", 0, ssl=self.ssl_context)
        scheme = "https" if self.ssl_context else "http"
        self.endpoint = f"{scheme}://127.0.0.1:{self.server.sockets[0].getsockname()[1]}"
        self.ack_server = None
        self.ack_endpoint = self.endpoint + "/ack"
        if self.ssl_context:
            self.ack_server = await asyncio.start_server(lambda reader, writer: self.handle(reader, writer, ack_only=True),
                                                         "127.0.0.1", 0)
            self.ack_endpoint = f"http://127.0.0.1:{self.ack_server.sockets[0].getsockname()[1]}/ack"
        return self

    async def __aexit__(self, *_args):
        self.server.close()
        await self.server.wait_closed()
        if self.ack_server:
            self.ack_server.close()
            await self.ack_server.wait_closed()
        self.release.set()
        self.list_release.set()
        self.put_release.set()
        for writer in tuple(self.writers):
            writer.close()
        async with asyncio.timeout(3):
            await asyncio.gather(*self.tasks, return_exceptions=True)

    def values(self, credentials=None, token=TOKEN):
        access, secret = credentials or self.first
        return {"GREGALE_S3_ASSETS_ENDPOINT": self.endpoint, "GREGALE_S3_ASSETS_REGION": self.region,
                "GREGALE_S3_ASSETS_BUCKET": "assets", "GREGALE_S3_ASSETS_ACCESS_KEY_ID": access,
                "GREGALE_S3_ASSETS_SECRET_ACCESS_KEY": secret, "GREGALE_S3_ASSETS_ADDRESSING_STYLE": "path",
                "STORAGE_APP_TOKEN": token}

    def authenticate(self, method, target, headers, body):
        match = re.fullmatch(r"AWS4-HMAC-SHA256 Credential=([^/]+)/([^,]+), SignedHeaders=([^,]+), Signature=([a-f0-9]{64})",
                             headers.get("authorization", ""))
        if not match or "x-amz-security-token" in headers:
            return None
        access, scope, signed, signature = match.groups()
        if access not in self.keys or not scope.endswith(f"/{self.region}/s3/aws4_request"):
            return None
        payload_hash = headers.get("x-amz-content-sha256")
        if payload_hash and payload_hash != "UNSIGNED-PAYLOAD" and payload_hash != hashlib.sha256(body).hexdigest():
            return None
        request = AWSRequest(method=method, url=self.endpoint + target,
                             headers={name: headers[name] for name in signed.split(";")}, data=body)
        request.context["timestamp"] = headers["x-amz-date"]
        signer = S3SigV4Auth(Credentials(access, self.keys[access]), "s3", self.region)
        expected = signer.signature(signer.string_to_sign(request, signer.canonical_request(request)), request)
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
    async def reply(writer, status, body=b""):
        writer.write(f"HTTP/1.1 {status} Fixture\r\nContent-Length: {len(body)}\r\nConnection: close\r\n\r\n".encode() + body)
        await writer.drain()

    async def handle(self, reader, writer, *, ack_only=False):
        task = asyncio.current_task()
        self.tasks.add(task)
        self.writers.add(writer)
        slow = False
        try:
            async with asyncio.timeout(20):
                lines = (await reader.readuntil(b"\r\n\r\n")).decode().split("\r\n")
                method, target, _version = lines[0].split(" ")
                headers = {key.lower(): value.strip() for key, value in
                           (line.split(":", 1) for line in lines[1:] if line)}
                if headers.get("expect", "").lower() == "100-continue":
                    writer.write(b"HTTP/1.1 100 Continue\r\n\r\n")
                    await writer.drain()
                body = await reader.readexactly(int(headers.get("content-length", "0")))
                if method == "POST" and target == "/ack":
                    self.acks.append(json.loads(body))
                    await self.reply(writer, 202)
                    return
                if ack_only:
                    await self.reply(writer, 403)
                    return
                access = self.authenticate(method, target, headers, body)
                path = unquote(urlsplit(target).path)
                self.requests.append((method, path, access))
                self.tls_requests.append(writer.get_extra_info("ssl_object") is not None)
                if access is None:
                    await self.reply(writer, 403, b"<Error><Code>AccessDenied</Code></Error>")
                elif method == "GET" and path == "/assets":
                    self.list_entered.set()
                    if self.hold_list and not await self.gate(reader, self.list_release):
                        return
                    await self.reply(writer, 403 if self.fail_list else 200,
                                     b"<Error><Code>AccessDenied</Code></Error>" if self.fail_list else
                                     b'<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">'
                                     b"<Name>assets</Name><MaxKeys>1</MaxKeys><IsTruncated>false</IsTruncated></ListBucketResult>")
                elif method == "PUT" and path.startswith("/assets/"):
                    self.puts.append((path, body, access))
                    self.objects[path.removeprefix("/assets/")] = body
                    self.put_entered.set()  # Commit before response: uncertain outcome test.
                    if self.hold_put and not await self.gate(reader, self.put_release):
                        return
                    await self.reply(writer, 200)
                elif method == "GET" and path == "/assets/slow.bin":
                    slow = True
                    writer.write(f"HTTP/1.1 200 OK\r\nContent-Length: {len(self.data)}\r\nConnection: close\r\n\r\n".encode())
                    writer.write(self.data[:65536])
                    await writer.drain()
                    self.stream_started.set()
                    if await self.gate(reader, self.release):
                        writer.write(self.data[65536:])
                        await writer.drain()
                elif method == "GET" and path == "/assets/broken.bin":
                    writer.write(b"HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\nshort")
                    await writer.drain()
                elif method == "GET" and path.removeprefix("/assets/") in self.objects:
                    await self.reply(writer, 200, self.objects[path.removeprefix("/assets/")])
                else:
                    await self.reply(writer, 404, b"<Error><Code>NoSuchKey</Code></Error>")
        except (ConnectionError, asyncio.IncompleteReadError):
            pass
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


def projection(root, server):
    env = {"FAAS_SECRETS_SNAPSHOT_FILE": str(root / "secrets.json"),
           "FAAS_SECRETS_RELOAD_READY_FILE": str(root / "ready"),
           "FAAS_SECRETS_RELOAD_ACK_ENDPOINT": server.ack_endpoint,
           "FAAS_SECRETS_RELOAD_GENERATION": "a" * 32}

    def publish(revision, values):
        temporary = root / "next.json"
        temporary.write_text(json.dumps({"revision": revision * 64, "secrets": values}))
        temporary.chmod(0o600)
        os.replace(temporary, env["FAAS_SECRETS_SNAPSHOT_FILE"])

    publish("a", server.values())
    return env, publish
