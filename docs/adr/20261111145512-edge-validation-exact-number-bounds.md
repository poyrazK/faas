# ADR-20261111145512: Exact numbers with bounded edge validation arithmetic

- Status: accepted
- Date: 2026-11-11
- Related: ADR-091, edge audit ER-015

## Context

Decoding schemas and request instances into float64 changes their meaning:
adjacent integers above 2^53 compare equal, and a fraction close to an integer
can pass an integer or maximum constraint. Preserving JSON number tokens fixes
those errors. The schema library then converts numbers to arbitrary-precision
rationals, where a short exponent can request disproportionate arithmetic or
exceed the library's supported conversion range. The request body ceiling does
not bound this expansion.

## Decision

Preserve number tokens in both schemas and instances. Before schema compilation
or instance validation, reject numeric tokens longer than 4096 bytes or decimal
exponents outside [-4096, 4096]. The constants live in pkg/api/limits.go and are
mirrored in the standalone validator with a consistency test.

Also bound the sum of numeric token bytes and absolute decimal exponents within
one document: at most the 64 KiB schema ceiling for schemas and the 25 MiB
platform body ceiling for request instances. This prevents many individually
bounded numbers from creating disproportionate aggregate arithmetic. The guard
does not perform the arithmetic it measures.

Schema violations are API validation errors before persistence. Instance
violations follow the rule's block/observe/warn behavior, just like other schema
mismatches. The guard walks decoded data without converting it to big integers
or rationals. Tests use small boundary fixtures and do not construct expensive
numeric values.

## Consequences

Numeric checks preserve practical integer and decimal precision while bounding
each rational conversion. Extremely long numbers and exponents require an
application-level representation, such as a string, rather than gateway numeric
validation. Existing schema/body size and combinator-depth bounds still apply.
