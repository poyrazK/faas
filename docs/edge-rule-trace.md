# Reusable edge-rule trace scenarios

`gregale edge-rules trace` accepts either individual request flags or a
versioned JSON scenario. Scenario files make a trace easy to replay from a
terminal, check into a private test repository, or share with a teammate.

```sh
gregale edge-rules trace --config trace.json
```

The version 1 format is:

```json
{
  "version": 1,
  "app": "demo",
  "request": {
    "url": "https://api.example.com/submit",
    "method": "POST",
    "headers": [
      "Content-Type: application/json",
      "X-Region: west",
      "X-Region: east"
    ],
    "client_ip": "203.0.113.10",
    "country": "US",
    "body": "{\"count\":3}"
  }
}
```

`app` and `request.url` are required. `method` defaults to `GET`; headers,
client IP, country, and body are optional. The `headers` array preserves
repeated values. `body` is UTF-8 text; use `body_base64` instead for arbitrary
bytes. Set at most one of those fields. Supplying an empty body still marks the
body as present. Request bodies are limited to 1 MiB, and config files to 2
MiB.

The trace uses the URL's host and path; it does not evaluate query parameters.
Scenario files are plain text. Keep credentials and sensitive body data out of
public repositories even though the resulting trace redacts credential-like
headers and withholds the body.

```json
{
  "version": 1,
  "app": "demo",
  "request": {
    "url": "https://api.example.com/upload",
    "method": "PUT",
    "headers": ["Content-Type: application/octet-stream"],
    "body_base64": "AAEC/w=="
  }
}
```

Unknown fields and unsupported versions are rejected. Use `--config -` to read
the JSON from stdin. `--config` cannot be combined with individual request
flags; edit the scenario file when changing a request. The file is read locally
and is not uploaded. Gregale fetches the app's current rules, evaluates them
locally, withholds request-body contents, and redacts credential-like header
values from trace output.
