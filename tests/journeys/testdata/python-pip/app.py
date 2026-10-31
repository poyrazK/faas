"""TestPythonSourceDeploys probe: reports which interpreter serves it.

On the shared Python runner base sys.base_prefix is the base's Python
(/usr or /usr/local); with Railpack's toolchain in the app layer it is under
/mise.
"""
import json
import sys


def app(environ, start_response):
    body = json.dumps({
        "variant": "pip",
        "python": "%d.%d" % sys.version_info[:2],
        "base_prefix": sys.base_prefix,
        "prefix": sys.prefix,
    }).encode()
    start_response("200 OK", [("Content-Type", "application/json"), ("Content-Length", str(len(body)))])
    return [body]
