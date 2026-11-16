# ADR-20261115085307: Structured JSON input locations in CLI errors

Date: 2026-11-15
Status: Accepted

## Context

JSON flag inputs already report a safe human source:line:column diagnostic.
Agents must parse that text to locate a malformed payload. ADR-20261115082128
adds structured CLI-local parser metadata, which can carry locations without
adding another command or changing server/SDK contracts (UX specification §3.2).

## Decision

The shared JSON flag reader carries an explicit inline, file or stdin source.
A syntax failure returns a typed error retaining only its flag, source and
one-based line/column. It never retains payload bytes, parsed values or the
value-bearing standard-library SyntaxError. Its Error method preserves the
existing human diagnostic, including filename control escaping and EOF rules.

The shared local JSON error handler finds that type through wrapped errors
and adds cli.kind=invalid_json, cli.flag and cli.location. Location fields are
source, optional path, line and column. Only file inputs have a path. A real
file named stdin or inline remains source=file; labels are not inferred.
Known credential-bearing paths and paths not representable as valid UTF-8
are omitted instead of advertising a redacted or lossy path as usable.
Valid filename controls round-trip through JSON string escaping.

The CLI extension keeps its validated offline help_argv. Existing human
wording, codes, categories, exits, stdout/stderr and API request-ID behavior
remain unchanged. Filesystem failures and other non-syntax errors do not
acquire a location; API Problems take their existing handler. Metadata does
not read more input, call the API, execute suggestions or retry a submission.

## Consequences and validation

Consumers can locate malformed input without parsing prose. Existing UTF-8
character columns, CRLF/tab handling, truncated-input positions and valid
payload bytes/numeric precision stay intact. Public dispatcher tests cover
source forms, flag names, wrapped errors, safe paths and zero requests. The
existing human-location and valid-input tests remain unchanged. An isolated
built-CLI journey locates a broken file and follows offline help before any
submission. No new API field, CLI command or configuration is introduced.
