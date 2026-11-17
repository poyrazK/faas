# ADR 20261111170000 · One-click CLI login through a loopback callback

- **Status:** accepted
- **Date:** 2026-11-11
- **Related:** ADR-20261111110000 (CLI approval needs fresh proof); bug hunts 2026-10-27 H9 and 2026-10-28 H67

## Problem

`gregale login` showed a code, opened `/cli-auth?code=…` and asked for the
code twice: the browser page wanted it typed in (H9, so a login link someone
sends cannot authorize *their* terminal), and the terminal offered a "paste
code" prompt. The code was already in the URL, so users typed what they
could see. Signing in should take one click on the approval page.

## Decision

Use the loopback redirect native apps use for OAuth (RFC 8252 §7.3) on top of
the existing device-code row (H67):

1. The CLI listens on `127.0.0.1:<random port>` and mints its code with
   `{"device": true, "loopback_port": <port>}`. The server answers
   `loopback: true` and a URL `/cli-auth?code=…&port=…`.
2. Signed in (with the fresh proof from ADR-20261111110000), the page shows
   only **Authorize**. The code and port ride in hidden fields.
3. Approving claims the code **and replaces its device code** with a new
   32-byte one in the same statement (`ClaimCliAuthCodeForDevice`). The
   response page sends the browser to
   `http://127.0.0.1:<port>/callback?code=…&device_code=<new>` (by script
   with a link fallback, because CSP `form-action 'self'` blocks a form
   POST that redirects to another origin).
4. The CLI checks the callback's code is its own, exchanges the new device
   code and saves the login. The browser tab says the terminal is signed in.

The phishing guard holds without a typed code: the new device code goes only
to the approving browser's own machine. A phisher who minted the code and
sends the link keeps the old device code, which no longer exchanges (410),
and cannot reach the victim's 127.0.0.1.

The typed-code flow stays for a browser on another computer:
`gregale login --device`, chosen automatically over SSH (`SSH_CONNECTION`,
`SSH_TTY`, `SSH_CLIENT`) or when the loopback listener cannot bind. The
terminal no longer offers a "paste code" prompt in any flow.

## Compatibility

- Older CLIs never send `loopback_port` and keep the typed flow.
- A new CLI against an older server gets 400 (unknown field) and mints
  again without the port; a server that ignores the port answers without
  `loopback` and the CLI uses the typed flow with that code.
- No migration: the claim rewrites the existing `device_hash` column.

## Consequences

- One click when the browser is signed in recently; a stale session still
  goes through sign-in or step-up first and returns to the same page (the
  port survives the re-auth redirect).
- The callback accepts only `GET /callback` with `Host: 127.0.0.1:<port>`
  and this login's code, so a web page that finds the port cannot log the
  CLI into another account without the 32-bit code shown nowhere but the
  approving browser's URL.
- `gregale start` uses the same sign-in.

## Amendment 2026-11-15: the device code needs the minting CLI's verifier

Account audit F10-3: the approval hands the device code to
`http://127.0.0.1:<port>/callback` in a URL, so it can stay in the browser's
history, an extension's view, or the request log of whatever listens on that
port, and on its own it redeemed a 30-day full-access key.

- When minting a login code (`device: true`, typed or one-click), the CLI also
  sends `code_challenge`: the SHA-256 of a 32-byte verifier it keeps in
  memory. `cli_auth_codes.verifier_hash` stores it (expand migration).
- The exchange sends `code_verifier`. A row with a `verifier_hash` is consumed
  only when the verifier hashes to it. A missing or wrong verifier leaves the
  row as it is: still pending reads as pending, approved reads as 410, so a
  leaked device code yields nothing and the real CLI can still finish. The
  loopback approval replaces only the device hash, so the binding survives it.
- Skew: a server that predates challenges refuses the unknown field with 400,
  and the CLI mints once more without it. Rows without a challenge keep the
  device-code-only exchange, so older CLIs are unaffected. Refusing
  challenge-less login codes is a later step, once older CLIs have aged out.
- Not covered: whoever minted the code holds the verifier, so a phisher who
  sends their own login link and can also read the request log of a local
  server on the victim's port still gets in. The page shows the code and port
  for that case (ADR-20261115160000).
