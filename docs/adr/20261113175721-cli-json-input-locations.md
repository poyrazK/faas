# ADR-20261113175721: Locate invalid CLI JSON input

Status: Accepted
Date: 2026-11-13
Related: ADR-20261111145512 (exact JSON numbers)

## Decision

The shared JSON flag resolver reports the flag, input source, line and column
when syntax validation fails. Sources are the selected filename, `stdin`, or
`inline`. Positions are one-based; columns count Unicode code points and a tab
counts as one character. LF and CRLF line endings are supported. An incomplete
document points to the next input position at EOF.

Keep the existing `must be valid JSON` diagnostic and flag name. Human and JSON
error output carry the same location in the error detail; error codes and exit
codes keep their existing contracts. File and stdin read failures retain their
existing wrapped I/O errors.

Use the standard JSON syntax offset only on the invalid-input path. Do not
include the parser's message, a payload excerpt, keys or values in diagnostics.
Escape non-printing filename characters so a filename cannot inject terminal
control sequences or additional diagnostic lines. The ordinary CLI credential
redactor still applies to rendered errors.

Valid inputs keep the existing validation path and return their original bytes;
do not decode values into floating-point numbers or re-encode payloads. Existing
empty/whitespace handling remains unchanged. This is a local diagnostic change:
there are no new flags, API fields, size limits or server execution changes.

## Verification

Table tests cover inline, file and stdin inputs; multiline syntax errors, Unicode
columns, CRLF, tabs, trailing documents and EOF; filename controls; and byte
preservation for valid input, including large integers and exponents. Built-CLI
journeys check human/JSON errors, value privacy and zero API requests on invalid
input, plus delivery of a valid payload without numeric conversion.
