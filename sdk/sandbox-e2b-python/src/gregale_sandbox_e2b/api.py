"""HTTP access to Gregale sandbox sessions (ADR-20261027090013 §3).

The control plane takes the API key; the data plane takes the session's
token. Only the standard library is used.
"""

from __future__ import annotations

import json
import os
import socket
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable, Dict, Iterator, Mapping, Optional, Protocol

from .errors import (
    AuthenticationException,
    InvalidArgumentException,
    NotEnoughSpaceException,
    NotFoundException,
    RateLimitException,
    SandboxException,
    SandboxNotFoundException,
    TimeoutException,
    UnsupportedException,
)

DEFAULT_API_BASE = "https://api.gregale.dev"


class Response(Protocol):
    """What a transport answers: a status, headers and a readable body."""

    status: int
    headers: Mapping[str, str]

    def read(self, amt: Optional[int] = None) -> bytes: ...

    def __iter__(self) -> Iterator[bytes]: ...

    def close(self) -> None: ...


# transport(method, url, headers, body, timeout) answers the response.
Transport = Callable[[str, str, Dict[str, str], Optional[bytes], Optional[float]], Response]


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    # A redirect would carry the bearer to another URL; the API never sends one.
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: ANN001
        return None


_opener = urllib.request.build_opener(_NoRedirect)


def urllib_transport(method: str, url: str, headers: Dict[str, str], body: Optional[bytes], timeout: Optional[float]) -> Response:
    req = urllib.request.Request(url, data=body, method=method, headers=headers)
    try:
        return _opener.open(req, timeout=timeout)
    except urllib.error.HTTPError as err:
        return err  # type: ignore[return-value]
    except (socket.timeout, TimeoutError) as err:
        raise TimeoutException(f"{method} {urllib.parse.urlsplit(url).path} timed out") from err
    except urllib.error.URLError as err:
        if isinstance(err.reason, (socket.timeout, TimeoutError)):
            raise TimeoutException(f"{method} {urllib.parse.urlsplit(url).path} timed out") from err
        raise


class ApiError(Exception):
    """The API's answer to a refused request."""

    def __init__(self, status: int, problem: Dict[str, Any]) -> None:
        super().__init__(problem.get("detail") or problem.get("title") or f"Gregale API error {status}")
        self.status = status
        self.code: str = problem.get("code") or ""
        self.problem = problem


def to_sandbox_error(err: BaseException, what: str) -> BaseException:
    """An API refusal as the E2B-style exception a caller catches."""
    if not isinstance(err, ApiError):
        return err
    detail = f"{what}: {err}" + (f" ({err.code})" if err.code else "")
    if err.status == 401:
        return AuthenticationException(detail)
    if err.code == "sandbox_not_found" or (err.status == 404 and err.code == ""):
        return SandboxNotFoundException(detail)
    if err.code == "sandbox_entry_not_found" or err.status == 404:
        return NotFoundException(detail)
    if err.code == "sandbox_unsupported":
        return UnsupportedException(what, str(err))
    if err.status == 507:
        return NotEnoughSpaceException(detail)
    if err.status == 429 or err.code == "agent_limit":
        return RateLimitException(detail)
    if err.status == 504:
        return TimeoutException(detail)
    if err.status in (400, 422):
        return InvalidArgumentException(detail)
    return SandboxException(detail)


class Api:
    def __init__(
        self,
        api_key: Optional[str] = None,
        api_base: Optional[str] = None,
        request_timeout: Optional[float] = None,
        transport: Optional[Transport] = None,
    ) -> None:
        key = api_key or os.environ.get("GREGALE_API_KEY")
        if not key:
            raise AuthenticationException("a Gregale API key is needed: pass api_key or set GREGALE_API_KEY")
        self._api_key = key
        self.api_base = (api_base or os.environ.get("GREGALE_API_BASE") or DEFAULT_API_BASE).rstrip("/")
        self.request_timeout = request_timeout
        self._transport = transport or urllib_transport

    def control(self, method: str, path: str, **args: Any) -> Any:
        res = self.send(method, path, self._api_key, **args)
        try:
            if res.status == 204:
                return None
            return json.loads(res.read() or b"null")
        finally:
            res.close()

    def data(self, method: str, path: str, token: str, **args: Any) -> Response:
        return self.send(method, path, token, **args)

    def send(
        self,
        method: str,
        path: str,
        bearer: str,
        query: Optional[Mapping[str, Any]] = None,
        json_body: Any = None,
        body: Optional[bytes] = None,
        content_type: Optional[str] = None,
        accept: str = "application/json",
        timeout: Optional[float] = None,
    ) -> Response:
        url = self.api_base + path
        params = {k: _query_value(v) for k, v in (query or {}).items() if v is not None}
        if params:
            url += "?" + urllib.parse.urlencode(params)
        headers = {"Authorization": f"Bearer {bearer}", "Accept": accept}
        data: Optional[bytes] = None
        if json_body is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps({k: v for k, v in json_body.items() if v is not None} if isinstance(json_body, dict) else json_body).encode()
        elif body is not None:
            headers["Content-Type"] = content_type or "application/octet-stream"
            data = body
        res = self._transport(method, url, headers, data, timeout if timeout is not None else self.request_timeout)
        if not 200 <= res.status < 300:
            try:
                problem = json.loads(res.read())
                if not isinstance(problem, dict):
                    problem = {"status": res.status}
            except ValueError:
                problem = {"status": res.status}
            finally:
                res.close()
            raise ApiError(res.status, problem)
        return res


def _query_value(v: Any) -> str:
    if isinstance(v, bool):
        return "true" if v else "false"
    return str(v)
