# ADR-20261123210931: Serve the Task inbox and reviewed input in the Python starter

Date: 2026-11-24
Status: Accepted

## Decision

The Python PostgreSQL starter serves the existing Gregale inbox and
reviewed-input extensions on modern external OAuth endpoints. The inbox
requires the same additive PostgreSQL index as Node and Go. It filters on
namespace, retained owner identities, current tool access, and expiry
before paging metadata. Signed cursors preserve PostgreSQL microseconds.

The reviewed write verifies the pending form revision under the Task row
lock and checks retention again after acquiring the lock. It reuses the
existing encrypted response state. After a lost reply, confirmation
compares the recorded answer and form revision without changing the Task.
The reviewed routes preserve exact JSON numbers when signing forms and
comparing answers.

Start a fresh scan and export a new review after changing runtime or
owner keys.
