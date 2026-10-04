"""Hello-world handler for gregale (Flask).

Served by gunicorn (see Procfile), which binds 0.0.0.0:$PORT when the
platform sets PORT. `python handler.py` still runs Flask's development
server for local use only. Returns a tiny
JSON greeting so a curl check is enough to verify a deploy landed.
Secrets set with `gregale env push` arrive as environment variables;
check which keys an app has with `gregale secrets list --app <slug>`.
"""
import os
import platform

from flask import Flask, jsonify

app = Flask(__name__)


@app.get("/")
def root():
    # This URL is public: do not echo environment variable names here
    # (secret names reveal integrations). `gregale secrets list --app
    # <slug>` shows which keys the app has.
    return jsonify(
        message="hello from gregale",
        python=platform.python_version(),
    )


@app.get("/healthz")
def healthz():
    return jsonify(ok=True)


if __name__ == "__main__":
    # Local development only; production traffic is served by gunicorn.
    port = int(os.environ.get("PORT", "8080"))
    app.run(host="0.0.0.0", port=port)  # noqa: S104 — guest-only listener