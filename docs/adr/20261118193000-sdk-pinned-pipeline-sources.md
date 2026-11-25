# ADR-20261118193000 Version-pinned inputs for typed pipeline starts

A preview may include an owned immutable managed-file reference. The preview
signature binds the reference alongside the existing input and summary. The
accepted native input includes the reference and its complete envelope digest
stays in the tool receipt. A separate logical request digest identifies an
authorized replay of the original input after the source pointer changes.

Node and Python workflow contexts expose the validated reference across
invocations. The worker read checks current application access and verifies
the exact version, byte length, and digest. An authorized Task reader receives
a typed source failure when a run terminates on access denial or an unavailable
version. Old receipts and envelopes retain their original shape.
