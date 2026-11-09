"""Bounded HTTP/1 request framing for the stateless MCP endpoint."""

import re


class RequestBodyError(ValueError):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


def _line(stream, budget):
    # Match the standard library's 64 KiB HTTP header-line bound, while also
    # bounding aggregate chunk metadata by the existing request-size limit.
    line = stream.readline(min(65537, budget + 1))
    if len(line) > budget or len(line) > 65536:
        raise RequestBodyError(413, "request framing exceeds the request limit")
    if not line.endswith(b"\r\n"):
        raise RequestBodyError(400, "invalid or incomplete chunk framing")
    return line[:-2], len(line)


def _trailers(stream, budget):
    while True:
        line, used = _line(stream, budget)
        budget -= used
        if not line:
            return
        name, separator, _value = line.partition(b":")
        if (not separator or not re.fullmatch(rb"[!#$%&'*+.^_`|~0-9A-Za-z-]+", name)
                or name.lower() in {b"content-length", b"transfer-encoding"}):
            raise RequestBodyError(400, "invalid request trailer")
        # Trailers are consumed for framing only, never merged into headers used
        # for authentication, content type or MCP dispatch.


def _chunked(stream, limit):
    body = bytearray()
    budget = limit
    while True:
        line, used = _line(stream, budget)
        budget -= used
        size, _separator, _extensions = line.partition(b";")
        if not re.fullmatch(rb"[0-9A-Fa-f]+", size):
            raise RequestBodyError(400, "invalid chunk size")
        length = int(size, 16)
        if length == 0:
            _trailers(stream, budget)
            return bytes(body)
        if length > limit - len(body):
            raise RequestBodyError(413, "request body exceeds 1 MiB")
        data = stream.read(length)
        if len(data) != length or stream.read(2) != b"\r\n":
            raise RequestBodyError(400, "invalid or incomplete chunk data")
        body.extend(data)


def read_request_body(stream, headers, limit):
    lengths = headers.get_all("Content-Length", [])
    encodings = headers.get_all("Transfer-Encoding", [])
    if len(lengths) > 1 or len(encodings) > 1 or (lengths and encodings):
        raise RequestBodyError(400, "ambiguous request framing")
    if encodings:
        if encodings[0].strip(" \t").lower() != "chunked":
            raise RequestBodyError(400, "unsupported transfer encoding")
        body = _chunked(stream, limit)
    else:
        value = lengths[0].strip(" \t") if lengths else ""
        if not re.fullmatch(r"[0-9]+", value):
            raise RequestBodyError(400, "invalid content length")
        # Avoid unbounded decimal conversion for a malformed, huge header.
        significant = value.lstrip("0") or "0"
        if len(significant) > len(str(limit)) or int(significant) > limit:
            raise RequestBodyError(413, "request body exceeds 1 MiB")
        length = int(significant)
        body = stream.read(length)
        if len(body) != length:
            raise RequestBodyError(400, "incomplete request body")
    if not body:
        raise RequestBodyError(413, "request body is missing")
    return body
