# ADR-20261115153114: Offline verification of CLI list exports

Status: Accepted
Date: 2026-11-15

## Context

Atomic list exports provide exact-byte SHA-256, byte count and record count in
completion receipts. A saved receipt should also be usable after copying or
archiving the file, without authenticating or reading live inventory again.

## Decision

Add `gregale export verify FILE --receipt RECEIPT`. Both arguments name local
regular files. Raw `-` is refused; use `./-` for a file with that name. Existing
interspersed flag parsing supports flags before or after FILE and literal `--`.
Dispatch before connection preflight and selected-profile validation, as other
local utilities do. Global option validation retains its existing behavior.
No API, credential, SDK or server contract changes are involved.

Require exactly one JSON receipt object with unique keys, format `jsonl`,
published true, nonnegative integer pages_completed/items_emitted/bytes_written,
and a 64-hex-character SHA-256 digest. All six fields must be present and non-null.
Accept uppercase digests and future fields. Ignore the optional receipt path:
it never selects a file and is never copied into a result or diagnostic.

Read the named export one line at a time. Each nonempty physical line must be
one valid UTF-8 JSON value. Blank lines, multiline values and multiple values
on one line are rejected with a line number; content is never echoed. Empty
files are valid with zero records. CRLF, surrounding JSON whitespace and a final
record without a newline are accepted, but exact bytes, including line endings,
are hashed. There is no API row-schema validation or numeric reserialization.
Memory follows one record, not the complete inventory; records have no added
size cap. Inputs are opened read-only, checked as regular files, and not changed.

For valid inputs, stdout reports all three independent checks and observed
hash/byte/record counts. Effective JSON mode (including FAAS_JSON) emits schema
version 1 with verified, format, checks and observed; explicit zero/false values
are retained. Human output reports success or which checks matched. Exit 0
requires all three matches; mismatch returns 1 with a complete comparison result.
Invalid input/read failure uses the existing local Problem or human error path,
returns no comparison result and exits 1. Output failures retain shared error
classification and native signal behavior; torn stdout remains possible.

The receipt's page count is validated but cannot be derived from JSON Lines.
Verification claims only the bytes read against the supplied integrity reference.
It is not a signature, origin attestation, point-in-time API snapshot or a file
lock against concurrent writers. Do not follow paths or re-fetch data from a
receipt, normalize files, silently repair them or automatically rerun exports.

## Verification

Core tests cover actual readers/files, exact bytes and large integer payloads,
empty/large/final/CRLF records, independent mismatches, invalid/duplicate/missing
receipt fields, malformed UTF-8/JSON Lines, read failures, private input isolation,
JSON/human output and short/broken writes. Public CLI tests create real list
exports for both commands and follow their receipts without login or new API
requests, plus help/discovery, environment JSON, explicit profile selection,
argument refusal and error contracts. A built-CLI isolated journey repeats
export/verify/tamper/error behavior against a local mock only.

Full CLI, generated-reference and journey qualification requires the complete
hosted exact-head checks; a standalone core pass does not qualify these layers.
