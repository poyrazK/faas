"""Plain Flask app with no tracing code (ADR-958, ADR-213 acceptance).

Each /checkout request logs one error line with the standard logging module,
which the platform bootstrap must export with the request's trace.
"""
import logging
import os

from flask import Flask

app = Flask(__name__)
log = logging.getLogger("checkout")


@app.get("/healthz")
def healthz():
    return ""


@app.get("/checkout")
def checkout():
    log.error("charge failed for buyer@example.com: card declined")
    # FAAS_TRACING_ENABLED proves guest-init stamped the bootstrap env.
    return "python " + os.environ.get("FAAS_TRACING_ENABLED", "") + "\n"


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.environ.get("PORT", "8080")))
