#!/usr/bin/env python3
"""Report successful scheduled canaries to an independent missing-run monitor.

The success URL is a credential. Never log it, response bodies or exceptions.
Only first-attempt scheduled runs with a successful job and probe may ping.
"""

import http.client
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def eligible(environ):
    return (environ.get("GITHUB_EVENT_NAME") == "schedule"
            and environ.get("GITHUB_RUN_ATTEMPT") == "1"
            and environ.get("CANARY_JOB_STATUS") == "success"
            and environ.get("CANARY_PROBE_OUTCOME") == "success")


def validate_url(value):
    if len(value) > 2048 or any(ord(c) < 33 or ord(c) > 126 for c in value) or "\\" in value:
        raise ValueError("invalid_url")
    url = urllib.parse.urlsplit(value)
    if (url.scheme != "https" or not url.hostname or url.username is not None
            or url.password is not None or url.fragment or url.port == 0):
        raise ValueError("invalid_url")
    return value


def report(url, opener=None, sleep=time.sleep):
    opener = opener or urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    request = urllib.request.Request(url, data=b"", method="POST", headers={"User-Agent": "gregale-canary-heartbeat/1"})
    for attempt in range(3):
        try:
            with opener.open(request, timeout=5) as response:
                if 200 <= response.status < 300:
                    return True
                if response.status < 500 and response.status != 429:
                    return False
        except urllib.error.HTTPError as err:
            err.close()
            if err.code < 500 and err.code != 429:
                return False
        except (OSError, http.client.HTTPException):
            pass
        if attempt < 2:
            sleep(1)
    return False


def run(environ, reporter=report):
    if not eligible(environ):
        print("canary-heartbeat: skipped_ineligible_run")
        return 0
    value = environ.get("CANARY_HEARTBEAT_URL", "")
    if not value:
        print("::error::canary-heartbeat: heartbeat_url_unconfigured", file=sys.stderr)
        return 1
    try:
        delivered = reporter(validate_url(value))
    except ValueError:
        print("::error::canary-heartbeat: invalid_heartbeat_url", file=sys.stderr)
        return 1
    except (OSError, http.client.HTTPException):
        delivered = False
    if not delivered:
        print("::error::canary-heartbeat: heartbeat_delivery_failed", file=sys.stderr)
        return 1
    print("canary-heartbeat: scheduled_success_reported")
    return 0


if __name__ == "__main__":
    sys.exit(run(os.environ))
