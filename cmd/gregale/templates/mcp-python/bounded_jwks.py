"""Bounded, single-flight JWKS refreshes that never hold the cache lock on I/O."""
import http.client
import json
import socket
import threading
import time
import urllib.error
import urllib.request

import jwt

MAX_JWKS_BYTES = 1 << 20


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        return None


class _RefreshHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, client, job):
        super().__init__(context=client.ssl_context)
        self.client, self.job = client, job

    def https_open(self, request):
        job = self.job

        class Connection(http.client.HTTPSConnection):
            def connect(connection):
                super().connect()
                with self.client._client_lock:
                    expired = job["expired"] or time.monotonic() >= job["deadline"]
                    # urllib detaches connection.sock after reading headers.
                    # Retain the socket so the body can still be interrupted.
                    job["socket"] = connection.sock
                if expired:
                    connection.close()
                    raise TimeoutError("JWKS refresh deadline exceeded")

        def factory(host, **kwargs):
            connection = Connection(host, **kwargs)
            with self.client._client_lock:
                job["connection"] = connection
            return connection

        return self.do_open(factory, request, context=self._context)


class BoundedJWKClient(jwt.PyJWKClient):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._refresh_job = None
        self._fetch_retry_at = 0

    def fetch_data(self):
        with self._client_lock:
            job = self._refresh_job
        handlers = [_NoRedirect(), _RefreshHTTPSHandler(self, job)]
        opener = urllib.request.build_opener(*handlers)
        request = urllib.request.Request(self.uri, headers=self.headers)
        try:
            with opener.open(request, timeout=self.timeout) as response:
                if response.status != 200:
                    raise ValueError("JWKS refresh failed")
                data = response.read(MAX_JWKS_BYTES + 1)
        except urllib.error.HTTPError as error:
            error.close()
            raise
        if len(data) > MAX_JWKS_BYTES:
            raise ValueError("JWKS response is too large")
        payload = json.loads(data)
        if not isinstance(payload, dict) or not isinstance(payload.get("keys"), list):
            raise ValueError("Invalid JWKS response")
        return payload

    def _run_refresh(self, job):
        result = None
        try:
            result = jwt.PyJWKSet.from_dict(self.fetch_data())
        except Exception:
            pass  # Authentication exposes only a stable, redacted failure.
        with self._client_lock:
            now = time.monotonic()
            expired = job["expired"] or now >= job["deadline"]
            if result is not None and not expired:
                if self.jwk_set_cache is not None:
                    self.jwk_set_cache.put(result)
                self._last_successful_fetch = now
                self._fetch_retry_at = 0
                job["result"] = result
            else:
                retry_from = job["deadline"] if expired else now
                self._fetch_retry_at = max(self._fetch_retry_at, retry_from + 30)
            self._refresh_job = None
            job["done"].set()

    def get_jwk_set(self, refresh=False):
        with self._client_lock:
            if self.jwk_set_cache is not None and not refresh:
                cached = self.jwk_set_cache.get()
                if cached is not None:
                    return cached
            now = time.monotonic()
            if now < self._fetch_retry_at:
                raise jwt.PyJWKClientConnectionError("JWKS refresh is cooling down")
            job = self._refresh_job
            if job is None:
                job = {"done": threading.Event(), "deadline": now + self.timeout,
                       "result": None, "expired": False, "connection": None, "socket": None}
                self._refresh_job = job
                # A stuck resolver cannot create a new thread for every request.
                threading.Thread(target=self._run_refresh, args=(job,), daemon=True).start()
        if not job["done"].wait(max(0, job["deadline"] - time.monotonic())):
            with self._client_lock:
                job["expired"] = True
                self._fetch_retry_at = max(self._fetch_retry_at, job["deadline"] + 30)
                sock = job["socket"]
            # shutdown interrupts header/body trickles without waiting on the
            # response reader's lock. DNS can outlive the deadline, but stays
            # confined to this one worker and cannot publish a late result.
            if sock is not None:
                try:
                    sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
            raise jwt.PyJWKClientConnectionError("JWKS refresh deadline exceeded")
        if job["result"] is None:
            raise jwt.PyJWKClientConnectionError("JWKS refresh failed")
        return job["result"]
