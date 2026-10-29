#!/usr/bin/env python3
"""Helpers for scripts/ci/obs_stack_smoke.sh.

  replay RATE WAKE_ID   write slog-shaped JSON lines to stdout at RATE lines/s
                        (run as a faas-* transient unit, so journald records
                        them and Alloy ships them); every 50th line carries
                        WAKE_ID so `gregalectl obs logs --wake-id` can find it
  sink PORT LOG         accept webhook POSTs on 127.0.0.1:PORT and append
                        "<path> <body>" to LOG, one line per request, so the
                        smoke can see Alertmanager deliver
"""

import http.server
import json
import sys
import time
import uuid


def replay(rate, wake_id):
    interval = 1.0 / rate
    n = 0
    next_at = time.monotonic()
    while True:
        rec = {
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "level": "INFO",
            "msg": "obs smoke replay",
            "request_id": uuid.uuid4().hex[:16],
            "wake_id": wake_id if n % 50 == 0 else str(uuid.uuid4()),
            "trace_id": uuid.uuid4().hex,
        }
        sys.stdout.write(json.dumps(rec) + "\n")
        sys.stdout.flush()
        n += 1
        next_at += interval
        delay = next_at - time.monotonic()
        if delay > 0:
            time.sleep(delay)


def sink(port, log_path):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", "0") or 0))
            with open(log_path, "a", encoding="utf-8") as log:
                log.write(f"{self.path} {body.decode('utf-8', 'replace')}\n")
            self.send_response(200)
            self.end_headers()

        def log_message(self, *args):
            pass

    http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()


def main(argv):
    if len(argv) == 3 and argv[0] == "replay":
        replay(float(argv[1]), argv[2])
    elif len(argv) == 3 and argv[0] == "sink":
        sink(int(argv[1]), argv[2])
    else:
        print(__doc__, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
