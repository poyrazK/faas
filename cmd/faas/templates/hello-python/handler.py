"""Hello-world handler for faas (Flask).

Listens on :8080 (the port guest-init forwards to). Returns a tiny
JSON greeting so a curl check is enough to verify a deploy landed.
The handler reads env vars (set via `faas env push`) and reports
the key NAMES only — never values — so a stdout log scrape can't leak
plaintext.
"""
import os

from flask import Flask, jsonify

app = Flask(__name__)


@app.get("/")
def root():
    # Skip env vars the runner sets itself; surface only customer keys.
    skip = {"PATH", "HOME", "LANG", "PYTHONUNBUFFERED", "FAAS_APP", "FAAS_DEPLOY"}
    secret_keys = sorted(
        k for k in os.environ.keys()
        if not k.startswith("FAAS_") and k not in skip
    )
    return jsonify(
        message="hello from faas",
        python=os.environ.get("PYTHON_VERSION", "unknown"),
        secret_keys=secret_keys,
    )


@app.get("/healthz")
def healthz():
    return jsonify(ok=True)


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8080"))
    app.run(host="0.0.0.0", port=port)  # noqa: S104 — guest-only listener