#!/usr/bin/env python3
"""Expand/contract linter for new migrations (docs/release-compatibility.md).

Units deploy independently, so the schema a migration produces is read by the
release that wrote it AND by the previous release still running beside it.
Every migration a change adds must therefore be online-safe for both:

  drop-table, drop-column, rename-table, rename-column
      A table, view or column may be dropped or renamed only once no code in
      the previous release reads it. The linter greps the previous release
      tag (and this change) for the name; a hit fails. Stop reading it in one
      release, drop it in the next.
  column-type
      ALTER COLUMN ... TYPE rewrites the table under an exclusive lock and
      changes what the previous release decodes. Add a column instead.
  not-null
      ADD COLUMN ... NOT NULL needs a DEFAULT, and SET NOT NULL needs a
      default set in the same migration: the previous release inserts rows
      without the column.
  table-rewrite
      ADD COLUMN with a volatile default (gen_random_uuid(), clock_timestamp()
      ...) or a STORED generated column rewrites the whole table under an
      ACCESS EXCLUSIVE lock.
  lock-timeout
      Any statement that takes a strong lock on an existing table (ALTER
      TABLE, CREATE/DROP INDEX without CONCURRENTLY, CREATE/DROP TRIGGER,
      CREATE OR REPLACE VIEW, LOCK, ...) must come after
      `SET LOCAL lock_timeout = '<= 10s'`. Without it the ALTER queues behind
      one long transaction and every query on the table queues behind the
      ALTER: a migration becomes an outage.
  index-build
      CREATE INDEX, ADD PRIMARY KEY or ADD UNIQUE on an existing table blocks
      writes for the whole build. Use CREATE INDEX CONCURRENTLY in a
      `-- +goose NO TRANSACTION` migration, or waive it with the measured
      cost (the table is small).
  concurrently-in-transaction
      CONCURRENTLY cannot run inside a transaction; goose wraps every file in
      one unless it says `-- +goose NO TRANSACTION`.

No migration may need writers stopped or a maintenance window
(ADR-202610171900, amendment 1). Four rules hold that line; none can be
waived, because the online pattern always exists:

  constraint-validation
      ADD CONSTRAINT ... FOREIGN KEY or CHECK on an existing table checks
      every row while holding a lock that blocks writes. Add it NOT VALID
      (new writes are checked at once) and VALIDATE CONSTRAINT it in a later
      transaction; validation does not block writes. A VALIDATE in the
      transaction that adds the constraint, an ADD COLUMN with a CHECK, and
      an ADD COLUMN ... REFERENCES with a DEFAULT validate under the lock too.
  constraint-churn
      One migration both drops and adds constraints of one existing table.
      Replace a constraint in three transactions: add the new one NOT VALID
      under a new name, validate it, then drop the old one (separate
      migrations, or goose statements of a NO TRANSACTION migration in that
      order). Attaching a concurrently built index (ADD CONSTRAINT ... USING
      INDEX) is exempt.
  unbatched-write
      An UPDATE or DELETE of an existing table must take its rows from a
      `SELECT ... FROM <that table> ... LIMIT n` (n at most MAX_BATCH_ROWS,
      or a PL/pgSQL variable) in its FROM, USING or WHERE clause or in a
      WITH query of the same statement, or set every column of one of the
      table's unique keys to constants in its WHERE. A loop of batches must
      run in a `-- +goose NO TRANSACTION` migration and COMMIT after each
      batch; inside one transaction every batch keeps its row locks until
      commit.
  lock-fanout
      One transaction takes write-blocking locks on more than
      MAX_LOCKED_TABLES existing tables. Each lock is held until commit
      while the next one is awaited: that is how a migration deadlocks
      against live writers. A foreign key locks the table it references.
      In a NO TRANSACTION migration every goose statement is its own
      transaction.

In a `-- +goose NO TRANSACTION` migration goose runs every statement as its
own transaction, on one pinned connection: a session-level lock_timeout
(SET without LOCAL) holds for the rest of the file, but a SET LOCAL or a
set_config(..., true) covers only the goose statement (a DO block, or a
StatementBegin/StatementEnd block) that sets it.

Tables, views and indexes that do not exist on the base (schema.sql there)
are exempt: nothing old reads them and nothing queues on them.

Only the `-- +goose Up` section is checked, and only migrations this change
adds: issued migrations are append-only history.

A finding of the other rules is waived by a comment anywhere in the
migration naming the rule, optionally the object, and a reason:

    -- migration-lint: allow index-build events: ~1 s write pause on 394k rows

Every new migration also declares its compatibility class, once, anywhere in
the file (docs/adr/20261018084150-unit-lane-expand-migrations.md):

    -- migration-compat: expand     the previous release runs correctly
                                    against the new schema; a per-unit
                                    release may apply it ahead of restarts
    -- migration-compat: contract   only a full release applies it

  compat-marker
      The marker is missing, repeated or neither value; or a migration
      marked expand makes a contract change (a drop or rename of anything
      that existed before, a column type change, a waived not-null, a
      TRUNCATE, a DROP of a function, type, sequence, trigger or unique
      constraint it does not create again), or waives lock-timeout or
      concurrently-in-transaction, which the lane needs to apply it
      unattended. This rule cannot be waived.

Usage:
  check_migration_expand_contract.py [--base REF] [--release auto|REF]
  check_migration_expand_contract.py --base REF FILE...   # lint given files
"""
from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
from math import prod
import sys
from dataclasses import dataclass, field

MAX_LOCK_TIMEOUT_MS = 10_000
# Existing tables one transaction may lock against writes: a table and the
# table its foreign key references, plus one more for a second parent or a
# trigger. Lock-order deadlocks need two tables; every table added beyond
# this keeps the earlier locks held while it waits.
MAX_LOCKED_TABLES = 3
# Rows one batch of a backfill may lock until it commits.
MAX_BATCH_ROWS = 10_000
# Changes that only stopping writers would make safe (ADR-202610171900,
# amendment 1). No waiver applies to them.
ONLINE_RULES = frozenset({"constraint-validation", "constraint-churn", "unbatched-write", "lock-fanout"})

# Paths whose mention of a table or column is not a read by a running
# release: history, generated schema projections, tests and docs.
REFERENCE_EXCLUDES = [
    ":!migrations/**",
    ":!**/schema.sql",
    ":!schema.sql",
    ":!**/sqlc/models.go",
    ":!**/*_test.go",
    ":!**/testdata/**",
    ":!pkg/db/pgtest/**",
    ":!cmd/e2e/**",
    ":!tests/**",
    ":!scripts/**",
    ":!docs/**",
]
REFERENCE_INCLUDES = ["*.go", "*.sql"]

VOLATILE_DEFAULTS = re.compile(
    r"\b(gen_random_uuid|uuid_generate_v[14]|random|clock_timestamp|timeofday|nextval|txid_current)\s*\(",
    re.IGNORECASE,
)
WAIVER = re.compile(
    r"--\s*migration-lint:\s*allow\s+(?P<rule>[a-z-]+)(?:\s+(?P<target>[A-Za-z0-9_.*]+))?\s*:\s*(?P<reason>\S.*)$",
    re.MULTILINE,
)
COMPAT_MARKER = re.compile(r"^--\s*migration-compat:[ \t]*(?P<value>.*?)[ \t]*$", re.MULTILINE)
COMPAT_VALUES = ("expand", "contract")
# Changes the previous release may not survive. In a lane verdict they count
# whether or not code references the object and whether or not they are
# waived: a waiver is a reviewer's judgment, which a full release carries.
CONTRACT_RULES = frozenset({
    "drop-table", "drop-column", "rename-table", "rename-column", "column-type", "not-null", "contract-statement",
})
# Rules the unit lane relies on to apply a migration with nobody watching:
# fail fast instead of queueing traffic, and run at all.
UNATTENDED_RULES = frozenset({"lock-timeout", "concurrently-in-transaction"})
ROUTINE_KINDS = ("function", "procedure", "routine", "aggregate")
# Objects a DROP removes for good; the previous release may call or use them.
DROPPED_OBJECT_KINDS = ("type", "domain", "sequence", "schema", "extension", "policy", "rule")
STATEMENT_STARTERS = {
    "alter", "create", "drop", "lock", "reindex", "cluster", "vacuum", "set",
    "perform", "update", "insert", "delete", "truncate", "select", "comment", "grant", "with", "commit",
}
PLPGSQL_OPENERS = {"begin", "then", "else", "loop", "declare"}


@dataclass
class Token:
    kind: str  # word, ident (quoted), string, dollar, punct, number, op
    value: str
    line: int


@dataclass
class Finding:
    path: str
    line: int
    rule: str
    target: str
    message: str


@dataclass
class Catalogue:
    """Relations and indexes that exist on the base. unique names the
    unique indexes and the primary key, unique and exclusion constraints:
    ON CONFLICT targets of the previous release."""

    relations: set[str] = field(default_factory=set)
    indexes: set[str] = field(default_factory=set)
    unique: set[str] = field(default_factory=set)
    index_tables: dict[str, str] = field(default_factory=dict)
    # table -> column sets of its primary key, unique constraints and
    # non-partial unique indexes over plain columns.
    unique_keys: dict[str, list[frozenset[str]]] = field(default_factory=dict)


# ---------------------------------------------------------------- tokenizer


def tokenize(sql: str, line: int = 1) -> list[Token]:
    tokens: list[Token] = []
    i, n = 0, len(sql)
    while i < n:
        c = sql[i]
        if c == "\n":
            line += 1
            i += 1
        elif c.isspace():
            i += 1
        elif sql.startswith("--", i):
            j = sql.find("\n", i)
            i = n if j < 0 else j
        elif sql.startswith("/*", i):
            j = sql.find("*/", i + 2)
            j = n if j < 0 else j + 2
            line += sql.count("\n", i, j)
            i = j
        elif c == "'" or (c in "eE" and sql.startswith("'", i + 1)):
            start = i
            i += 1 if c == "'" else 2
            while i < n:
                if sql[i] == "'" and sql.startswith("''", i):
                    i += 2
                elif sql[i] == "\\" and c in "eE":
                    i += 2
                elif sql[i] == "'":
                    i += 1
                    break
                else:
                    i += 1
            text = sql[start:i]
            tokens.append(Token("string", text, line))
            line += text.count("\n")
        elif c == '"':
            j = i + 1
            while j < n:
                if sql.startswith('""', j):
                    j += 2
                elif sql[j] == '"':
                    break
                else:
                    j += 1
            tokens.append(Token("ident", sql[i + 1 : j].replace('""', '"'), line))
            i = j + 1
        elif c == "$":
            m = re.match(r"\$([A-Za-z_][A-Za-z0-9_]*)?\$", sql[i:])
            if m:
                tag = m.group(0)
                end = sql.find(tag, i + len(tag))
                end = n if end < 0 else end
                body = sql[i + len(tag) : end]
                tokens.append(Token("dollar", body, line))
                line += sql.count("\n", i, end + len(tag))
                i = end + len(tag)
            else:
                m = re.match(r"\$\d+", sql[i:])
                tokens.append(Token("op", m.group(0) if m else "$", line))
                i += len(m.group(0)) if m else 1
        elif c.isalpha() or c == "_":
            m = re.match(r"[A-Za-z_][A-Za-z0-9_$]*", sql[i:])
            tokens.append(Token("word", m.group(0).lower(), line))
            i += len(m.group(0))
        elif c.isdigit():
            m = re.match(r"\d+(\.\d+)?", sql[i:])
            tokens.append(Token("number", m.group(0), line))
            i += len(m.group(0))
        elif c in "(),;.[]":
            tokens.append(Token("punct", c, line))
            i += 1
        else:
            m = re.match(r"[-+*/<>=~!@#%^&|`?:]+", sql[i:])
            text = m.group(0) if m else c
            tokens.append(Token("op", text, line))
            i += len(text)
    return tokens


def split_statements(tokens: list[Token]) -> list[list[Token]]:
    """Split on top-level semicolons; DO blocks contribute their inner
    statements too, since they run at migration time."""
    return [stmt for top in top_statements(tokens) for stmt in expand(top)]


def top_statements(tokens: list[Token]) -> list[list[Token]]:
    """Split on top-level semicolons, leaving DO blocks whole."""
    statements: list[list[Token]] = []
    current: list[Token] = []
    depth = 0
    for tok in tokens:
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth = max(0, depth - 1)
        if tok.kind == "punct" and tok.value == ";" and depth == 0:
            if current:
                statements.append(current)
            current = []
            continue
        current.append(tok)
    if current:
        statements.append(current)
    return statements


def do_body(stmt: list[Token]) -> list[Token] | None:
    """The tokens of a DO block's body, or None for any other statement."""
    if stmt and is_word(stmt[0], "do"):
        bodies = [t for t in stmt if t.kind == "dollar"]
        if bodies:
            return tokenize(bodies[0].value, bodies[0].line)
    return None


def expand(stmt: list[Token]) -> list[list[Token]]:
    body = do_body(stmt)
    if body is None:
        return [stmt]
    inner: list[list[Token]] = []
    for chunk in split_statements(body):
        inner.extend(plpgsql_statements(chunk))
    return inner


def plpgsql_statements(chunk: list[Token]) -> list[list[Token]]:
    """Find the SQL statement in a PL/pgSQL chunk such as
    `BEGIN IF NOT EXISTS (...) THEN ALTER TABLE ...`."""
    depth = 0
    start = None
    for idx, tok in enumerate(chunk):
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        elif depth == 0 and tok.kind == "word" and tok.value in STATEMENT_STARTERS:
            if idx == 0 or (chunk[idx - 1].kind == "word" and chunk[idx - 1].value in PLPGSQL_OPENERS):
                start = idx
    if start is None:
        return []
    return [chunk[start:]]


def is_word(tok: Token, *values: str) -> bool:
    return tok.kind == "word" and tok.value in values


def words(stmt: list[Token]) -> list[str]:
    return [t.value for t in stmt]


def name_at(stmt: list[Token], i: int) -> tuple[str, int]:
    """Read a possibly schema-qualified name starting at i; return the bare
    relation name and the index after it."""
    if i >= len(stmt):
        return "", i
    name = stmt[i].value
    i += 1
    while i + 1 < len(stmt) and stmt[i].kind == "punct" and stmt[i].value == ".":
        name = stmt[i + 1].value
        i += 2
    return name, i


def skip(stmt: list[Token], i: int, *seq: str) -> int:
    """Advance past an optional keyword sequence such as IF NOT EXISTS."""
    j = i
    for word in seq:
        if j < len(stmt) and is_word(stmt[j], word):
            j += 1
        else:
            return i
    return j


def split_actions(stmt: list[Token], start: int) -> list[list[Token]]:
    actions: list[list[Token]] = []
    current: list[Token] = []
    depth = 0
    for tok in stmt[start:]:
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        if tok.kind == "punct" and tok.value == "," and depth == 0:
            actions.append(current)
            current = []
            continue
        current.append(tok)
    if current:
        actions.append(current)
    return actions


def top_level_words(tokens: list[Token]) -> list[str]:
    out, depth = [], 0
    for tok in tokens:
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        elif depth == 0 and tok.kind == "word":
            out.append(tok.value)
    return out


def has_sequence(seq: list[str], *want: str) -> bool:
    k = len(want)
    return any(seq[i : i + k] == list(want) for i in range(len(seq) - k + 1))


def parse_duration_ms(value: str) -> int | None:
    m = re.fullmatch(r"\s*(\d+(?:\.\d+)?)\s*(ms|s|min|h)?\s*", value.strip("'\""))
    if not m:
        return None
    number = float(m.group(1))
    return int(number * {None: 1, "ms": 1, "s": 1000, "min": 60_000, "h": 3_600_000}[m.group(2)])


# ------------------------------------------------------------------- linter


@dataclass
class DoBlock:
    """What a DO block's body does around the statements in it."""

    loops: bool
    commits: bool
    # (variable, table) -> batch size, for `SELECT ... INTO variable` from a
    # query that reads table with a LIMIT: the keys of one batch.
    batches: dict[tuple[str, str], int] = field(default_factory=dict)


def do_block(body: list[Token]) -> DoBlock:
    block = DoBlock(loops=any(is_word(t, "loop") for t in body), commits=any(is_word(t, "commit") for t in body))
    for chunk in split_statements(body):
        for stmt in plpgsql_statements(chunk):
            if not is_word(stmt[0], "select", "with"):
                continue
            into = next((i for i in range(len(stmt) - 1)
                         if is_word(stmt[i], "into") and depth_at(stmt, 0, i) == 0 and stmt[i + 1].kind in ("word", "ident")), None)
            if into is None:
                continue
            var = stmt[into + 2] if is_word(stmt[into + 1], "strict") and into + 2 < len(stmt) else stmt[into + 1]
            for i in range(len(stmt)):
                size = limit_size(stmt, i)
                if size is None:
                    continue
                for table in limit_sources(stmt, i):
                    key = (var.value, table)
                    block.batches[key] = min(size, block.batches.get(key, size))
    return block


@dataclass
class Context:
    path: str
    catalogue: Catalogue
    no_transaction: bool
    referenced: object  # callable(name, table|None) -> (where, files)
    findings: list[Finding] = field(default_factory=list)
    lock_timeout_group: int | None = None
    # A session-level lock_timeout (SET without LOCAL, or set_config(...,
    # false)) holds for every later statement: goose runs a migration on one
    # pinned connection.
    session_lock_timeout: bool = False
    created: set[str] = field(default_factory=set)
    created_indexes: set[str] = field(default_factory=set)
    defaults_set: set[tuple[str, str]] = field(default_factory=set)
    recreated: set[str] = field(default_factory=set)
    # The transaction the current statement runs in: one per migration, or
    # one per goose statement (and per COMMIT) under NO TRANSACTION.
    group: int = 0
    do_block: DoBlock | None = None
    # group -> existing table -> line of the first write-blocking lock.
    locks: dict[int, dict[str, int]] = field(default_factory=dict)
    # existing table -> (group, "add" | "validate" | "drop", line) of each
    # constraint change, in order.
    constraint_changes: dict[str, list[tuple[int, str, int]]] = field(default_factory=dict)
    # (group, table, name) of every constraint added.
    added_constraints: set[tuple[int, str, str]] = field(default_factory=set)

    def exists(self, relation: str) -> bool:
        return relation in self.catalogue.relations and relation not in self.created

    def add(self, line: int, rule: str, target: str, message: str) -> None:
        self.findings.append(Finding(self.path, line, rule, target, message))

    def need_lock_timeout(self, line: int, what: str, relation: str, table: str | None = None) -> None:
        """Record a write-blocking lock on table (default: relation) and
        require a lock_timeout in force for the transaction taking it."""
        if (table or relation) != "*":
            self.locks.setdefault(self.group, {}).setdefault(table or relation, line)
        # One finding per relation: the fix is a single SET at the top.
        if (self.lock_timeout_group != self.group and not self.session_lock_timeout
                and not any(f.rule == "lock-timeout" and f.target == relation for f in self.findings)):
            fix = (f"add `SET LOCAL lock_timeout = '5s';` at the top of the Up section (at most {MAX_LOCK_TIMEOUT_MS // 1000}s)."
                   if not self.no_transaction else
                   "in a `-- +goose NO TRANSACTION` migration every goose statement is its own transaction, so a SET LOCAL "
                   "on its own line ends with it. Start the Up section with a session-level `SET lock_timeout = '5s';`, or "
                   "set it inside the same statement: `DO $$ BEGIN PERFORM set_config('lock_timeout', '5s', true); "
                   "ALTER TABLE ...; END $$;`.")
            self.add(line, "lock-timeout", relation,
                     f"{what} takes a strong lock on existing {relation} with no lock_timeout set before it; {fix}")


def up_section(text: str) -> tuple[str, int]:
    """Return the Up section and the line it starts on."""
    up = re.search(r"^--\s*\+goose\s+Up\b.*$", text, re.MULTILINE | re.IGNORECASE)
    start = up.end() if up else 0
    down = re.search(r"^--\s*\+goose\s+Down\b", text[start:], re.MULTILINE | re.IGNORECASE)
    end = start + down.start() if down else len(text)
    return text[start:end], text.count("\n", 0, start) + 1


def lint_migration(path: str, text: str, catalogue: Catalogue, referenced) -> list[Finding]:
    return apply_waivers(lint_context(path, text, catalogue, referenced).findings, text)


def lint_context(path: str, text: str, catalogue: Catalogue, referenced) -> Context:
    """Lint one migration; the context holds every finding before waivers
    and the relations and indexes the migration creates."""
    up, first_line = up_section(text)
    ctx = Context(
        path=path,
        catalogue=catalogue,
        no_transaction=bool(re.search(r"^--\s*\+goose\s+NO\s+TRANSACTION\b", text, re.MULTILINE | re.IGNORECASE)),
        referenced=referenced,
    )
    tops = top_statements(tokenize(up, first_line))
    statements = [stmt for top in tops for stmt in expand(top)]
    # Views dropped and created again in the same migration are redefined,
    # not removed.
    for stmt in statements:
        w = words(stmt)
        if w[:2] == ["create", "view"] or w[:4] == ["create", "or", "replace", "view"] or w[:3] == ["create", "materialized", "view"]:
            i = w.index("view") + 1
            i = skip(stmt, i, "if", "not", "exists")
            ctx.recreated.add(name_at(stmt, i)[0])
    blocks = statement_blocks(up, first_line)
    block = None
    for top in tops:
        if ctx.no_transaction:
            # goose sends a StatementBegin/StatementEnd block as one query
            # (one implicit transaction); every other statement on its own.
            mine = next((b for b in blocks if b[0] <= top[0].line <= b[1]), None)
            if mine is None or mine != block:
                ctx.group += 1
            block = mine
        body = do_body(top)
        ctx.do_block = None if body is None else do_block(body)
        for stmt in expand(top):
            lint_statement(ctx, stmt)
    lint_migration_locks(ctx)
    return ctx


def churn_line(changes: list[tuple[int, str, int]]) -> int | None:
    """Where a table's constraint changes break add -> validate -> drop, or
    None. No transaction (group) both adds and drops; every drop follows a
    validation of the latest constraint added before it; and no constraint
    is added after a drop that no earlier add replaced."""
    adds = [g for g, kind, _ in changes if kind == "add"]
    validates = [g for g, kind, _ in changes if kind == "validate"]
    if not adds:
        return None
    for group, kind, line in changes:
        if kind != "drop":
            continue
        prior = [g for g in adds if g < group]
        if group in adds:
            return line
        if prior and not any(max(prior) < g < group for g in validates):
            return line
        if not prior and any(g > group for g in adds):
            return line
    return None


def statement_blocks(up: str, first_line: int) -> list[tuple[int, int]]:
    """Line ranges of the StatementBegin/StatementEnd blocks in an Up section."""
    marks = [(first_line + up.count("\n", 0, m.start()), m.group(1).lower())
             for m in re.finditer(r"^--\s*\+goose\s+Statement(Begin|End)\b", up, re.MULTILINE | re.IGNORECASE)]
    blocks, start = [], None
    for line, kind in marks:
        if kind == "begin":
            start = line
        elif start is not None:
            blocks.append((start, line))
            start = None
    return blocks


def lint_migration_locks(ctx: Context) -> None:
    """constraint-churn and lock-fanout, which judge the whole migration."""
    for table, changes in ctx.constraint_changes.items():
        line = churn_line(changes)
        if line is not None:
            ctx.add(line, "constraint-churn", table,
                    f"this migration drops and adds constraints of existing {table}. Replace a constraint in three steps, "
                    "each its own transaction: add the new one NOT VALID under a new name, VALIDATE it, then drop the old "
                    "one. Use separate migrations, or separate goose statements of one `-- +goose NO TRANSACTION` "
                    "migration in that order. No waiver applies.")
    for tables in ctx.locks.values():
        if len(tables) > MAX_LOCKED_TABLES:
            over, line = list(tables.items())[MAX_LOCKED_TABLES]
            ctx.add(line, "lock-fanout", over,
                    f"one transaction takes write-blocking locks on {len(tables)} existing tables ({', '.join(tables)}); "
                    f"at most {MAX_LOCKED_TABLES}. Each lock is held until commit while the next is awaited, which is how a "
                    f"migration deadlocks against live writers. Split it into migrations of at most {MAX_LOCKED_TABLES} "
                    "existing tables each, or use a `-- +goose NO TRANSACTION` migration, where every statement commits on "
                    "its own. No waiver applies.")


def lint_statement(ctx: Context, stmt: list[Token]) -> None:
    if not stmt:
        return
    w = words(stmt)
    line = stmt[0].line

    if (w[:1] == ["set"] and "lock_timeout" in w[:3]) or set_config_lock_timeout(stmt) is not None:
        value = lock_timeout_value(stmt) if w[:1] == ["set"] else set_config_lock_timeout(stmt)
        ms = parse_duration_ms(value) if value is not None else None
        if ms is None or ms == 0 or ms > MAX_LOCK_TIMEOUT_MS:
            ctx.add(line, "lock-timeout", "*", f"lock_timeout {value!r} must be between 1ms and {MAX_LOCK_TIMEOUT_MS // 1000}s.")
        else:
            ctx.lock_timeout_group = ctx.group
            local = w[:2] == ["set", "local"] if w[:1] == ["set"] else set_config_is_local(stmt)
            ctx.session_lock_timeout = ctx.session_lock_timeout or not local
        return

    if w[0] == "commit":
        # A COMMIT in a DO block of a NO TRANSACTION migration starts a new
        # transaction, without the locks or the local settings of the last.
        if ctx.no_transaction:
            ctx.group += 1
        return

    if w[0] == "create":
        lint_create(ctx, stmt, w, line)
    elif w[0] == "alter" and len(w) > 1 and w[1] == "table":
        lint_alter_table(ctx, stmt, line)
    elif w[0] == "alter" and len(w) > 2 and w[1] in ("view", "materialized"):
        i = w.index("view") + 1
        i = skip(stmt, i, "if", "exists")
        relation, i = name_at(stmt, i)
        if ctx.exists(relation):
            ctx.need_lock_timeout(line, "ALTER VIEW", relation)
            for action in split_actions(stmt, i):
                lint_rename(ctx, action, relation, line)
    elif w[0] == "drop":
        lint_drop(ctx, stmt, w, line)
    elif w[0] in ("lock", "truncate"):
        i = skip(stmt, 1, "table")
        for relation in name_list(stmt, skip(stmt, i, "only")):
            if ctx.exists(relation):
                ctx.need_lock_timeout(line, w[0].upper(), relation)
    elif w[0] in ("update", "delete", "with"):
        lint_write(ctx, stmt, line)
    elif w[0] in ("reindex", "cluster") or w[:2] == ["vacuum", "full"]:
        if "concurrently" not in w:
            ctx.need_lock_timeout(line, w[0].upper(), w[-1] if len(w) > 1 else "*")


def lock_timeout_value(stmt: list[Token]) -> str | None:
    for idx, tok in enumerate(stmt):
        if is_word(tok, "lock_timeout"):
            rest = [t for t in stmt[idx + 1 :] if not (t.kind == "op" and t.value == "=") and not is_word(t, "to")]
            if not rest:
                return None
            value = "".join(t.value for t in rest)
            return value.strip("'")
    return None


def set_config_is_local(stmt: list[Token]) -> bool:
    """Whether a set_config('lock_timeout', ...) call is transaction-local
    (its third argument is not false)."""
    for idx in range(len(stmt) - 6):
        if is_word(stmt[idx], "set_config") and stmt[idx + 5].value == ",":
            return not is_word(stmt[idx + 6], "false")
    return True


def set_config_lock_timeout(stmt: list[Token]) -> str | None:
    """The value of a set_config('lock_timeout', '<value>', ...) call."""
    for idx in range(len(stmt) - 4):
        a, b, c, d, e = stmt[idx : idx + 5]
        if (is_word(a, "set_config") and b.value == "(" and c.kind == "string" and c.value.strip("'").lower() == "lock_timeout"
                and d.value == "," and e.kind == "string"):
            return e.value.strip("'")
    return None


def lint_create(ctx: Context, stmt: list[Token], w: list[str], line: int) -> None:
    if "table" in w[:4] and "index" not in w[:4]:
        i = w.index("table") + 1
        i = skip(stmt, i, "if", "not", "exists")
        relation, _ = name_at(stmt, i)
        if relation not in ctx.catalogue.relations:
            ctx.created.add(relation)
        for parent in referenced_tables(stmt):
            if parent != relation and ctx.exists(parent):
                ctx.need_lock_timeout(line, f"a FOREIGN KEY of {relation}", parent)
        return
    if "index" in w[:3]:
        i = w.index("index") + 1
        concurrently = i < len(stmt) and is_word(stmt[i], "concurrently")
        if concurrently:
            i += 1
        i = skip(stmt, i, "if", "not", "exists")
        index_name = ""
        if i < len(stmt) and not is_word(stmt[i], "on"):
            index_name, i = name_at(stmt, i)
        i = skip(stmt, i, "on")
        i = skip(stmt, i, "only")
        relation, _ = name_at(stmt, i)
        if index_name:
            ctx.created_indexes.add(index_name)
        if concurrently and not ctx.no_transaction:
            ctx.add(line, "concurrently-in-transaction", index_name or relation,
                    "CREATE INDEX CONCURRENTLY needs `-- +goose NO TRANSACTION` in this migration.")
        if ctx.exists(relation) and not concurrently:
            ctx.need_lock_timeout(line, "CREATE INDEX", relation)
            ctx.add(line, "index-build", relation,
                    f"CREATE INDEX on existing {relation} blocks writes for the whole build. Use CREATE INDEX CONCURRENTLY "
                    "in a `-- +goose NO TRANSACTION` migration, or waive with the measured table size.")
        return
    if "trigger" in w[:5]:
        on = w.index("on") if "on" in w else -1
        if on > 0:
            relation, _ = name_at(stmt, on + 1)
            if ctx.exists(relation):
                ctx.need_lock_timeout(line, "CREATE TRIGGER", relation)
        return
    if w[:4] == ["create", "or", "replace", "view"]:
        relation, _ = name_at(stmt, 4)
        if ctx.exists(relation):
            ctx.need_lock_timeout(line, "CREATE OR REPLACE VIEW", relation)


def lint_drop(ctx: Context, stmt: list[Token], w: list[str], line: int) -> None:
    if len(w) < 2:
        return
    if w[1] in ("table", "view") or w[1:3] == ["materialized", "view"]:
        i = 3 if w[1] == "materialized" else 2
        i = skip(stmt, i, "if", "exists")
        for relation in name_list(stmt, i):
            if not ctx.exists(relation):
                continue
            if relation in ctx.recreated:
                ctx.need_lock_timeout(line, "DROP VIEW", relation)
                continue
            ctx.need_lock_timeout(line, f"DROP {w[1].upper()}", relation)
            report_reference(ctx, line, "drop-table", relation, None, f"DROP of {relation}")
    elif w[1] == "index":
        i = 2
        concurrently = i < len(stmt) and is_word(stmt[i], "concurrently")
        if concurrently:
            i += 1
            if not ctx.no_transaction:
                ctx.add(line, "concurrently-in-transaction", "*", "DROP INDEX CONCURRENTLY needs `-- +goose NO TRANSACTION` in this migration.")
        i = skip(stmt, i, "if", "exists")
        for index in name_list(stmt, i):
            if index in ctx.catalogue.indexes and index not in ctx.created_indexes and not concurrently:
                ctx.need_lock_timeout(line, "DROP INDEX", index, table=ctx.catalogue.index_tables.get(index, index))
    elif w[1] == "trigger":
        on = w.index("on") if "on" in w else -1
        if on > 0:
            relation, _ = name_at(stmt, on + 1)
            if ctx.exists(relation):
                ctx.need_lock_timeout(line, "DROP TRIGGER", relation)


def name_list(stmt: list[Token], i: int) -> list[str]:
    names = []
    while i < len(stmt):
        if is_word(stmt[i], "cascade", "restrict"):
            break
        name, i = name_at(stmt, i)
        if name:
            names.append(name)
        if i < len(stmt) and stmt[i].kind == "punct" and stmt[i].value == ",":
            i += 1
        else:
            break
    return names


def lint_alter_table(ctx: Context, stmt: list[Token], line: int) -> None:
    i = skip(stmt, 2, "if", "exists")
    i = skip(stmt, i, "only")
    relation, i = name_at(stmt, i)
    if i < len(stmt) and stmt[i].kind == "op" and stmt[i].value == "*":
        i += 1
    if not ctx.exists(relation):
        # A new table's foreign key still locks the existing table it references.
        for parent in referenced_tables(stmt):
            if parent != relation and ctx.exists(parent):
                ctx.need_lock_timeout(line, f"a FOREIGN KEY of {relation}", parent)
        return
    actions = split_actions(stmt, i)
    blocking = any(top_level_words(a)[:2] != ["validate", "constraint"] for a in actions)
    if blocking:
        ctx.need_lock_timeout(line, "ALTER TABLE", relation)
    for action in actions:
        aw = top_level_words(action)
        if not aw:
            continue
        if aw[0] == "rename":
            lint_rename(ctx, action, relation, line)
        elif aw[:2] == ["drop", "constraint"]:
            ctx.constraint_changes.setdefault(relation, []).append((ctx.group, "drop", line))
        elif aw[:2] == ["validate", "constraint"]:
            ctx.constraint_changes.setdefault(relation, []).append((ctx.group, "validate", line))
            name = constraint_parts(action, "validate")[0]
            if (ctx.group, relation, name) in ctx.added_constraints:
                ctx.add(line, "constraint-validation", f"{relation}.{name}",
                        f"VALIDATE CONSTRAINT {name} in the transaction that adds it scans {relation} while the ADD's lock "
                        "blocks writes. Validate it in a later migration, or in a later goose statement of a "
                        "`-- +goose NO TRANSACTION` migration. No waiver applies.")
        elif aw[0] == "drop" and len(aw) > 1 and aw[1] != "constraint":
            j = 2 if aw[1] == "column" else 1
            rest = aw[j:]
            if rest[:2] == ["if", "exists"]:
                rest = rest[2:]
            if rest:
                report_reference(ctx, line, "drop-column", f"{relation}.{rest[0]}", relation, f"DROP COLUMN {relation}.{rest[0]}", column=rest[0])
        elif aw[0] == "alter":
            k = 2 if len(aw) > 2 and aw[1] == "column" else 1
            column = aw[k] if len(aw) > k else ""
            change = aw[k + 1 :]
            if change[:1] == ["type"] or change[:3] == ["set", "data", "type"]:
                ctx.add(line, "column-type", f"{relation}.{column}",
                        f"changing the type of {relation}.{column} rewrites the table under an exclusive lock and changes what the "
                        "previous release decodes. Add a new column, backfill, and move readers over.")
            elif change[:2] == ["set", "default"]:
                ctx.defaults_set.add((relation, column))
            elif change[:3] == ["set", "not", "null"] and (relation, column) not in ctx.defaults_set:
                ctx.add(line, "not-null", f"{relation}.{column}",
                        f"SET NOT NULL on {relation}.{column}: the previous release inserts rows without it. Set a DEFAULT in "
                        "this migration first, or waive it if every writer in the previous release already sets the column.")
        elif aw[0] == "add":
            lint_add(ctx, action, aw, relation, line)


def lint_add(ctx: Context, action: list[Token], aw: list[str], relation: str, line: int) -> None:
    for parent in referenced_tables(action):
        if parent != relation and ctx.exists(parent):
            ctx.need_lock_timeout(line, f"a FOREIGN KEY of {relation}", parent)
    if len(aw) > 1 and aw[1] in ("constraint", "primary", "unique", "foreign", "check", "exclude"):
        if ("primary" in aw or "unique" in aw) and not has_sequence(aw, "using", "index"):
            ctx.add(line, "index-build", relation,
                    f"ADD PRIMARY KEY/UNIQUE on existing {relation} builds an index under an ACCESS EXCLUSIVE lock. Build it "
                    "with CREATE UNIQUE INDEX CONCURRENTLY, then ADD CONSTRAINT ... USING INDEX.")
        name, kind = constraint_parts(action, "add")
        if not has_sequence(aw, "using", "index"):
            ctx.constraint_changes.setdefault(relation, []).append((ctx.group, "add", line))
        if name:
            ctx.added_constraints.add((ctx.group, relation, name))
        if kind in ("foreign", "check") and not has_sequence(aw, "not", "valid"):
            what = "FOREIGN KEY" if kind == "foreign" else "CHECK"
            ctx.add(line, "constraint-validation", f"{relation}.{name}" if name else relation,
                    f"ADD {what} {name} on existing {relation} checks every row while holding a lock that blocks writes. Add "
                    "it NOT VALID (new writes are checked at once), then VALIDATE CONSTRAINT in a later migration, which does "
                    "not block writes. No waiver applies.")
        return
    j = 2 if len(aw) > 1 and aw[1] == "column" else 1
    rest = aw[j:]
    if rest[:3] == ["if", "not", "exists"]:
        rest = rest[3:]
    column = rest[0] if rest else "?"
    target = f"{relation}.{column}"
    has_default = "default" in aw or has_sequence(aw, "generated")
    if has_sequence(aw, "not", "null") and not has_default:
        ctx.add(line, "not-null", target,
                f"ADD COLUMN {target} NOT NULL without a DEFAULT: existing rows and the previous release's inserts have no value for it.")
    raw = " ".join(t.value for t in action)
    if "default" in aw and VOLATILE_DEFAULTS.search(raw.replace(" (", "(")):
        ctx.add(line, "table-rewrite", target, f"ADD COLUMN {target} with a volatile DEFAULT rewrites {relation} under an exclusive lock. "
                "Add the column without a default, backfill in batches, then set the default.")
    if has_sequence(aw, "generated", "always") and "stored" in aw:
        ctx.add(line, "table-rewrite", target, f"a STORED generated column rewrites {relation} under an exclusive lock.")
    if "check" in rest[1:] or ("references" in rest[1:] and "default" in rest[1:]):
        ctx.add(line, "constraint-validation", target,
                f"ADD COLUMN {target} with a {'CHECK' if 'check' in rest[1:] else 'REFERENCES and a DEFAULT'} checks every row "
                f"of {relation} under an ACCESS EXCLUSIVE lock. Add the column bare, then ADD CONSTRAINT ... NOT VALID, and "
                "VALIDATE it in a later migration. No waiver applies.")


def constraint_parts(action: list[Token], verb: str) -> tuple[str, str]:
    """(name, kind) of `ADD [CONSTRAINT name] kind ...`, or (name, "") of
    `VALIDATE CONSTRAINT name`. Quoted names count."""
    top, depth = [], 0
    for tok in action:
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        elif depth == 0 and tok.kind in ("word", "ident"):
            top.append(tok)
    i = 1 if top and is_word(top[0], verb) else 0
    name = ""
    if i < len(top) and is_word(top[i], "constraint"):
        name = top[i + 1].value if i + 1 < len(top) else ""
        i += 2
    kind = top[i].value if i < len(top) and top[i].kind == "word" else ""
    return name, kind


def referenced_tables(tokens: list[Token]) -> list[str]:
    """The tables named by REFERENCES clauses: a foreign key takes a SHARE
    ROW EXCLUSIVE lock, which blocks writes, on the table it references."""
    return [name_at(tokens, i + 1)[0] for i, tok in enumerate(tokens) if is_word(tok, "references")]


# ------------------------------------------------------------ batched writes


def lint_write(ctx: Context, stmt: list[Token], line: int) -> None:
    """unbatched-write: every UPDATE or DELETE of an existing table takes
    its rows from a LIMITed SELECT of that table, and a loop of batches
    commits after each one."""
    for verb, start, end in write_spans(stmt):
        table = write_target(stmt, verb, start)
        if not ctx.exists(table):
            continue
        what = f"{verb.upper()} of existing {table}"
        bound = batch_bound(stmt, start, end, table)
        if bound is None:
            bound = keyed_rows(stmt, start, end, ctx.catalogue.unique_keys.get(table, []), table,
                               ctx.do_block.batches if ctx.do_block else {})
        if bound is None:
            ctx.add(line, "unbatched-write", table,
                    f"{what} is not batched: one statement locks every row it changes until the migration commits, and live "
                    f"writers queue behind it. Take the rows from `SELECT ... FROM {table} ... LIMIT n` (n <= {MAX_BATCH_ROWS}) "
                    "and loop in a `-- +goose NO TRANSACTION` migration that COMMITs after each batch "
                    "(docs/release-compatibility.md), or backfill from a job. No waiver applies.")
        elif bound > MAX_BATCH_ROWS:
            ctx.add(line, "unbatched-write", table,
                    f"{what} takes batches of {bound} rows; at most {MAX_BATCH_ROWS}. No waiver applies.")
        elif ctx.do_block and ctx.do_block.loops and not (ctx.no_transaction and ctx.do_block.commits):
            ctx.add(line, "unbatched-write", table,
                    f"{what} runs in a loop inside one transaction, so every batch keeps its row locks until the migration "
                    "commits. Put the DO block alone in a `-- +goose NO TRANSACTION` migration and COMMIT after each batch. "
                    "No waiver applies.")


def write_spans(stmt: list[Token]) -> list[tuple[str, int, int]]:
    """(verb, start, end) of every UPDATE and DELETE in a statement: the
    statement itself, a WITH query's main statement, or a data-modifying
    WITH query."""
    if not stmt:
        return []
    if is_word(stmt[0], "update", "delete"):
        return [(stmt[0].value, 0, len(stmt))]
    if not is_word(stmt[0], "with"):
        return []
    spans, opens, depth, main = [], [], 0, False
    for i, tok in enumerate(stmt):
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
            opens.append(i)
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
            start = opens.pop() if opens else -1
            if start >= 0 and start + 1 < len(stmt) and is_word(stmt[start + 1], "update", "delete"):
                spans.append((stmt[start + 1].value, start + 1, i))
        elif depth == 0 and not main and i > 0 and is_word(tok, "select", "insert", "update", "delete", "merge", "values"):
            main = True
            if tok.value in ("update", "delete"):
                spans.append((tok.value, i, len(stmt)))
    return spans


def write_target(stmt: list[Token], verb: str, start: int) -> str:
    i = skip(stmt, start + 1, "from") if verb == "delete" else start + 1
    return name_at(stmt, skip(stmt, i, "only"))[0]


def batch_bound(stmt: list[Token], start: int, end: int, table: str) -> int | None:
    """The batch size bounding an UPDATE or DELETE spanning [start, end):
    the LIMIT of a SELECT that reads the target table, in the statement's
    FROM, USING or WHERE clause or in a WITH query outside the span. 0 for
    a LIMIT given by a variable or WHERE CURRENT OF; None when unbounded."""
    region, depth, clause = [], 0, False
    for i in range(start, end):
        tok = stmt[i]
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        elif depth == 0 and is_word(tok, "returning"):
            break
        elif depth == 0 and is_word(tok, "from", "using", "where") and i > start + 1:
            clause = True
        if clause:
            region.append(i)
    if any(is_word(stmt[i], "current") and i + 1 < len(stmt) and is_word(stmt[i + 1], "of") for i in region):
        return 0
    region += [i for i in range(len(stmt)) if i < start or i >= end]
    bounds = [limit_size(stmt, i) for i in region if table in limit_sources(stmt, i)]
    bounds = [b for b in bounds if b is not None]
    return min(bounds) if bounds else None


def limit_size(stmt: list[Token], i: int) -> int | None:
    """The row count of a LIMIT at index i: its number, 0 for a variable,
    None for LIMIT ALL or a token that is not a LIMIT."""
    if not is_word(stmt[i], "limit") or i + 1 >= len(stmt):
        return None
    size = stmt[i + 1]
    if size.kind == "number":
        return int(float(size.value))
    if size.kind in ("word", "ident") and not is_word(size, "all"):
        return 0
    return None


def keyed_rows(stmt: list[Token], start: int, end: int, keys: list[frozenset[str]], table: str = "",
               batches: dict[tuple[str, str], int] | None = None) -> int | None:
    """At most how many rows a WHERE clause selects when it ANDs `column =
    constant` or `column IN (constants)` over every column of one unique
    key of the target, or ANDs `column = ANY (variable)` with a variable the
    DO block fills from a LIMITed query of the target (batches); None
    otherwise."""
    where = next((i for i in range(start, end) if is_word(stmt[i], "where") and depth_at(stmt, start, i) == 0), None)
    if where is None:
        return None
    conjuncts, current, depth = [], [], 0
    for tok in stmt[where + 1 : end]:
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        if depth == 0 and is_word(tok, "returning"):
            break
        if depth == 0 and is_word(tok, "or"):
            return None
        if depth == 0 and is_word(tok, "and"):
            conjuncts.append(current)
            current = []
            continue
        current.append(tok)
    conjuncts.append(current)
    pinned: dict[str, int] = {}
    for c in conjuncts:
        if len(c) > 2 and c[0].kind in ("word", "ident"):
            column, rest = c[0].value, c[1:]
            if len(c) > 4 and c[1].value == "." and c[2].kind in ("word", "ident"):
                column, rest = c[2].value, c[3:]
            if (len(rest) == 5 and rest[0].value == "=" and is_word(rest[1], "any") and rest[2].value == "("
                    and rest[3].kind in ("word", "ident") and rest[4].value == ")" and (rest[3].value, table) in (batches or {})):
                return (batches or {})[(rest[3].value, table)]
            if rest[0].kind == "op" and rest[0].value == "=" and constants(rest[1:]) == 1:
                pinned[column] = 1
            elif is_word(rest[0], "in") and rest[1].value == "(" and rest[-1].value == ")" and constants(rest[2:-1]):
                pinned[column] = constants(rest[2:-1])
    sizes = [prod(pinned[c] for c in key) for key in keys if key <= pinned.keys()]
    return min(sizes) if sizes else None


def constants(tokens: list[Token]) -> int:
    """How many constants a comma-separated list of literals holds (a
    string or number, optionally cast); 0 when anything else is in it."""
    n, expect, i = 0, True, 0
    while i < len(tokens):
        tok = tokens[i]
        if expect and tok.kind in ("string", "number"):
            n, expect = n + 1, False
            if i + 2 < len(tokens) and tokens[i + 1].kind == "op" and tokens[i + 1].value == "::":
                i += 2
        elif not expect and tok.kind == "punct" and tok.value == ",":
            expect = True
        else:
            return 0
        i += 1
    return 0 if expect else n


def depth_at(stmt: list[Token], start: int, index: int) -> int:
    depth = 0
    for tok in stmt[start:index]:
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
    return depth


def limit_sources(stmt: list[Token], limit: int) -> list[str]:
    """The tables the query that a LIMIT at index limit ends reads FROM:
    the innermost parenthesized query around it, or the statement."""
    if not is_word(stmt[limit], "limit"):
        return []
    depth, start = 0, 0
    for i in range(limit - 1, -1, -1):
        tok = stmt[i]
        if tok.kind == "punct" and tok.value == ")":
            depth += 1
        elif tok.kind == "punct" and tok.value == "(":
            if depth == 0:
                start = i + 1
                break
            depth -= 1
    depth, tables = 0, []
    for i in range(start, limit):
        tok = stmt[i]
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        elif depth == 0 and is_word(tok, "from"):
            tables.append(name_at(stmt, skip(stmt, i + 1, "only"))[0])
    return tables


def lint_rename(ctx: Context, action: list[Token], relation: str, line: int) -> None:
    aw = top_level_words(action)
    if not aw or aw[0] != "rename":
        return
    if aw[1:2] == ["to"]:
        report_reference(ctx, line, "rename-table", relation, None, f"RENAME of {relation}")
    elif aw[1:2] == ["constraint"]:
        return
    else:
        column = aw[2] if aw[1] == "column" else aw[1]
        report_reference(ctx, line, "rename-column", f"{relation}.{column}", relation, f"RENAME of {relation}.{column}", column=column)


def report_reference(ctx: Context, line: int, rule: str, target: str, table: str | None, what: str, column: str | None = None) -> None:
    name = column or target
    where, files = ctx.referenced(name, table)
    if not files:
        return
    shown = ", ".join(files[:5]) + (f" (+{len(files) - 5} more)" if len(files) > 5 else "")
    ctx.add(line, rule, target,
            f"{what}: still referenced by {where} in {shown}. Stop reading it in one release; drop or rename it in the next "
            "(expand/contract). Waive only if those hits are not reads.")


def apply_waivers(findings: list[Finding], text: str) -> list[Finding]:
    waivers = [(m.group("rule"), m.group("target")) for m in WAIVER.finditer(text)]
    kept = []
    for f in findings:
        if f.rule not in ONLINE_RULES and any(rule == f.rule and (target in (None, "*") or target == f.target or f.target.startswith(target + ".")) for rule, target in waivers):
            continue
        kept.append(f)
    return kept


# ------------------------------------------------------- compatibility class


def compat_marker(text: str) -> tuple[str | None, str | None]:
    """The migration's declared compatibility class, or the problem with
    its declaration."""
    values = [m.group("value") for m in COMPAT_MARKER.finditer(text)]
    if not values:
        return None, "declares no `-- migration-compat:` class"
    if len(values) > 1:
        return None, "declares `-- migration-compat:` more than once"
    if values[0] not in COMPAT_VALUES:
        return None, f"declares migration-compat {values[0]!r}, which is neither expand nor contract"
    return values[0], None


def every_release(name: str, table: str | None):
    """A referenced() for verdicts: any drop or rename of an existing
    object is a contract change, whoever reads it."""
    return "any release", ["*"]


def created_objects(statements: list[list[Token]]) -> set[tuple[str, ...]]:
    """Routines, triggers, indexes and constraints a migration creates, so a
    DROP it follows with a CREATE of the same name counts as a redefinition."""
    created: set[tuple[str, ...]] = set()
    for stmt in statements:
        w = words(stmt)
        if w[:1] == ["create"]:
            i = skip(stmt, 1, "or", "replace")
            i = skip(stmt, i, "constraint")
            i = skip(stmt, i, "unique")
            if i >= len(stmt):
                continue
            if is_word(stmt[i], *ROUTINE_KINDS):
                created.add(("routine", name_at(stmt, i + 1)[0]))
            elif is_word(stmt[i], "trigger"):
                name, j = name_at(stmt, i + 1)
                on = next((k for k in range(j, len(stmt)) if is_word(stmt[k], "on")), None)
                if on is not None:
                    created.add(("trigger", name, name_at(stmt, on + 1)[0]))
            elif is_word(stmt[i], "index"):
                j = skip(stmt, i + 1, "concurrently")
                j = skip(stmt, j, "if", "not", "exists")
                if j < len(stmt) and not is_word(stmt[j], "on"):
                    created.add(("index", name_at(stmt, j)[0]))
        elif w[:2] == ["alter", "table"]:
            table, i = alter_table_target(stmt)
            for action in split_actions(stmt, i):
                aw = top_level_words(action)
                if aw[:2] == ["add", "constraint"] and len(aw) > 2:
                    created.add(("constraint", table, aw[2]))
    return created


def alter_table_target(stmt: list[Token]) -> tuple[str, int]:
    i = skip(stmt, 2, "if", "exists")
    i = skip(stmt, i, "only")
    table, i = name_at(stmt, i)
    if i < len(stmt) and stmt[i].kind == "op" and stmt[i].value == "*":
        i += 1
    return table, i


def routine_names(stmt: list[Token], i: int) -> list[str]:
    """Names in `DROP FUNCTION a(int), b(text) CASCADE`."""
    names, depth, expect = [], 0, True
    while i < len(stmt):
        tok = stmt[i]
        if tok.kind == "punct" and tok.value == "(":
            depth += 1
        elif tok.kind == "punct" and tok.value == ")":
            depth -= 1
        elif depth == 0 and tok.kind == "punct" and tok.value == ",":
            expect = True
        elif depth == 0 and expect and tok.kind in ("word", "ident"):
            name, i = name_at(stmt, i)
            names.append(name)
            expect = False
            continue
        i += 1
    return names


def contract_statements(path: str, text: str, catalogue: Catalogue) -> list[Finding]:
    """Contract changes the expand/contract rules do not model: the removal
    or renaming of a routine, type, sequence, trigger or unique constraint,
    a dropped column default, and TRUNCATE."""
    up, first_line = up_section(text)
    statements = split_statements(tokenize(up, first_line))
    created = created_objects(statements)
    found: list[Finding] = []

    def add(stmt: list[Token], target: str, what: str) -> None:
        found.append(Finding(path, stmt[0].line, "contract-statement", target, what))

    for stmt in statements:
        w = words(stmt)
        if not w:
            continue
        if w[0] == "truncate":
            add(stmt, w[-1], "TRUNCATE deletes rows the previous release reads")
        elif w[0] == "drop" and len(w) > 1:
            i = skip(stmt, 2, "if", "exists")
            if w[1] in ROUTINE_KINDS:
                for name in routine_names(stmt, i):
                    if ("routine", name) not in created:
                        add(stmt, name, f"DROP {w[1].upper()} {name} without creating it again")
            elif w[1] in DROPPED_OBJECT_KINDS:
                for name in name_list(stmt, i):
                    add(stmt, name, f"DROP {w[1].upper()} {name}")
            elif w[1] == "trigger":
                name, j = name_at(stmt, i)
                table, _ = name_at(stmt, skip(stmt, j, "on"))
                if table in catalogue.relations and ("trigger", name, table) not in created:
                    add(stmt, f"{table}.{name}", f"DROP TRIGGER {name} on {table} without creating it again")
            elif w[1] == "index":
                i = skip(stmt, skip(stmt, 2, "concurrently"), "if", "exists")
                for name in name_list(stmt, i):
                    if name in catalogue.unique and ("index", name) not in created:
                        add(stmt, name, f"DROP INDEX of unique index {name}, an ON CONFLICT target")
        elif w[:2] == ["alter", "table"]:
            table, i = alter_table_target(stmt)
            for action in split_actions(stmt, i):
                aw = top_level_words(action)
                if aw[:2] == ["rename", "constraint"]:
                    add(stmt, table, f"RENAME CONSTRAINT on {table}")
                elif aw[:2] == ["drop", "constraint"]:
                    rest = aw[2:]
                    rest = rest[2:] if rest[:2] == ["if", "exists"] else rest
                    name = rest[0] if rest else ""
                    if name in catalogue.unique and ("constraint", table, name) not in created:
                        add(stmt, f"{table}.{name}", f"DROP CONSTRAINT {name}, a unique constraint and ON CONFLICT target")
                elif aw[:1] == ["alter"] and has_sequence(aw, "drop", "default"):
                    add(stmt, table, f"DROP DEFAULT on {table}: the previous release inserts rows relying on it")
        elif w[0] == "alter" and len(w) > 2 and w[1] not in ("view", "materialized") and "rename" in top_level_words(stmt):
            name = name_at(stmt, 2)[0]
            # A routine renamed aside and created again keeps its name.
            if not (w[1] in ROUTINE_KINDS and ("routine", name) in created):
                add(stmt, name, f"ALTER {w[1].upper()} {name} RENAME")
    return found


def is_waived(finding: Finding, text: str) -> bool:
    return not apply_waivers([finding], text)


def lane_findings(path: str, text: str, catalogue: Catalogue) -> tuple[list[Finding], list[Finding]]:
    """(blockers, waived) for applying a migration in a per-unit release.
    Blockers: every contract change, waived or not; every finding left
    after waivers; a waived lock-timeout or concurrently-in-transaction.
    Waived findings that stay eligible record a lock cost a reviewer
    accepted (index-build, table-rewrite)."""
    raw = lint_context(path, text, catalogue, every_release).findings + contract_statements(path, text, catalogue)
    blockers, waived = [], []
    for f in raw:
        if f.rule in CONTRACT_RULES or f.rule in UNATTENDED_RULES or not is_waived(f, text):
            blockers.append(f)
        else:
            waived.append(f)
    return blockers, waived


def check_compat_marker(path: str, text: str, catalogue: Catalogue) -> list[Finding]:
    """compat-marker findings for one new migration (never waivable)."""
    marker, problem = compat_marker(text)
    if problem:
        return [Finding(path, 1, "compat-marker", "*",
                        f"{path} {problem}. Add `-- migration-compat: expand` when the previous release runs correctly "
                        "against the new schema (a per-unit release may apply it), or `-- migration-compat: contract` "
                        "when only a full release may.")]
    if marker != "expand":
        return []
    out = []
    for f in lane_findings(path, text, catalogue)[0]:
        if f.rule in CONTRACT_RULES:
            out.append(Finding(path, f.line, "compat-marker", f.target,
                               f"marked expand, but {f.rule} {f.target} is a contract change the previous release may "
                               "not survive. Mark the migration `-- migration-compat: contract` so a full release applies it."))
        elif f.rule in UNATTENDED_RULES and is_waived(f, text):
            out.append(Finding(path, f.line, "compat-marker", f.target,
                               f"marked expand, but waives {f.rule}: a per-unit release applies migrations unattended and "
                               "needs it. Fix the finding, or mark the migration contract."))
    return out


def migration_version(path: str) -> int:
    m = re.match(r"(\d+)_", os.path.basename(path))
    return int(m.group(1)) if m else 0


def lane_verdict(path: str, text: str, catalogue: Catalogue) -> dict:
    """Whether a per-unit release may apply this migration ahead of
    restarts (docs/adr/20261018084150-unit-lane-expand-migrations.md)."""
    marker, problem = compat_marker(text)
    blockers, waived = lane_findings(path, text, catalogue)
    reasons = [problem] if problem else []
    if marker == "contract":
        reasons.append("declares migration-compat: contract")
    for f in blockers:
        if f.rule in CONTRACT_RULES:
            reasons.append(f"contract change {f.rule} {f.target} at line {f.line}")
        elif not is_waived(f, text):
            reasons.append(f"lint finding {f.rule} {f.target} at line {f.line}")
        else:
            reasons.append(f"waives {f.rule} {f.target}, which the lane needs to apply it unattended")
    return {
        "path": path,
        "version": migration_version(path),
        "sha256": hashlib.sha256(text.encode()).hexdigest(),
        "compat": marker,
        "eligible": not reasons,
        "blockers": reasons,
        "waived": sorted(f"{f.rule} {f.target}" for f in waived),
    }


def is_migration_file(path: str, directory: str = "migrations/") -> bool:
    """A goose migration: a .sql file directly in the migrations directory
    (migrations/embed.go embeds `*.sql`)."""
    return path.startswith(directory) and path.endswith(".sql") and "/" not in path[len(directory):]


def _union(have: set[str], more: set[str]) -> set[str]:
    return have if isinstance(have, _Everything) else have | more


def range_catalogues(base: Catalogue, texts: dict[str, str]) -> dict[str, Catalogue]:
    """Per migration: the base catalogue plus what the range's other
    migrations create. An earlier unit release may already have applied
    those and run code that reads them, so they are existing objects too."""
    made = {path: lint_context(path, text, base, every_release) for path, text in texts.items()}
    out = {}
    for path in texts:
        relations = set().union(*(ctx.created for other, ctx in made.items() if other != path))
        indexes = set().union(*(ctx.created_indexes for other, ctx in made.items() if other != path))
        out[path] = Catalogue(relations=_union(base.relations, relations), indexes=_union(base.indexes, indexes),
                              unique=_union(base.unique, indexes), index_tables=base.index_tables,
                              unique_keys=base.unique_keys)
    return out


def classify_range(repo: str, base: str, head: str, directory: str = "migrations/") -> dict:
    """The data step of a per-unit release over the full release at base:
    every migration added in (base, head], each with its lane verdict
    against base's schema. Eligible only when every one is, and when no
    issued migration was modified or deleted (append-only)."""
    status = git(repo, "diff", "--name-status", "--no-renames", base, head, "--", directory)
    added, altered = [], []
    for line in status.splitlines():
        state, _, path = line.partition("\t")
        if is_migration_file(path, directory):
            (added if state == "A" else altered).append(path)
    schema = git(repo, "show", f"{base}:schema.sql", check=False)
    catalogue = load_catalogue(schema) if schema else Catalogue(_Everything(), _Everything(), _Everything())
    texts = {path: git(repo, "show", f"{head}:{path}") for path in added}
    catalogues = range_catalogues(catalogue, texts)
    verdicts = [lane_verdict(path, texts[path], catalogues[path])
                for path in sorted(added, key=lambda p: (migration_version(p), p))]
    blockers = [f"{v['path']}: {reason}" for v in verdicts for reason in v["blockers"]]
    blockers += [f"{path}: an issued migration was modified or deleted" for path in sorted(altered)]
    return {"eligible": not blockers, "migrations": verdicts, "blockers": blockers}


# ---------------------------------------------------------------- git glue


def git(repo: str, *args: str, check: bool = True) -> str:
    proc = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True)
    if check and proc.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {proc.stderr.strip()}")
    return proc.stdout


def load_catalogue(schema_sql: str) -> Catalogue:
    cat = Catalogue()
    for m in re.finditer(r"^CREATE (?:UNLOGGED )?(?:TABLE|VIEW|MATERIALIZED VIEW) (?:\w+\.)?\"?(\w+)\"?", schema_sql, re.MULTILINE):
        cat.relations.add(m.group(1))
    for m in re.finditer(r"^CREATE (UNIQUE )?INDEX (\w+) ON (?:ONLY )?(?:\w+\.)?\"?(\w+)", schema_sql, re.MULTILINE):
        cat.indexes.add(m.group(2))
        cat.index_tables[m.group(2)] = m.group(3)
        if m.group(1):
            cat.unique.add(m.group(2))
    for m in re.finditer(r"^\s+ADD CONSTRAINT (\w+) (?:PRIMARY KEY|UNIQUE|EXCLUDE)\b", schema_sql, re.MULTILINE):
        cat.unique.add(m.group(1))
    keys = [(m.group(1), m.group(2)) for m in re.finditer(
        r"^ALTER TABLE ONLY (?:\w+\.)?\"?(\w+)\"?\n\s+ADD CONSTRAINT \w+ (?:PRIMARY KEY|UNIQUE(?: NULLS NOT DISTINCT)?) \(([^)]*)\);$",
        schema_sql, re.MULTILINE)]
    keys += [(m.group(1), m.group(2)) for m in re.finditer(
        r"^CREATE UNIQUE INDEX \w+ ON (?:ONLY )?(?:\w+\.)?\"?(\w+)\"? USING \w+ \(([^()]*)\);$", schema_sql, re.MULTILINE)]
    for table, columns in keys:
        names = [c.strip().strip('"') for c in columns.split(",")]
        if all(re.fullmatch(r"\w+", n) for n in names):
            cat.unique_keys.setdefault(table, []).append(frozenset(names))
    return cat


def make_referenced(repo: str, release: str):
    """Return referenced(name, table) -> (where, files): files in the
    previous release, else in this change, that mention name (and table)."""

    def grep(ref: str | None, word: str) -> set[str]:
        args = ["grep", "-l", "-w", "-F", "-e", word]
        args += [ref] if ref else ["--untracked"]
        args += ["--", *REFERENCE_INCLUDES, *REFERENCE_EXCLUDES]
        proc = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True)
        if proc.returncode not in (0, 1):
            raise RuntimeError(f"git grep {word}: {proc.stderr.strip()}")
        prefix = f"{ref}:" if ref else ""
        return {line[len(prefix):] for line in proc.stdout.splitlines() if line}

    def referenced(name: str, table: str | None):
        for where, ref in ((f"the previous release ({release})", release), ("this change", None)):
            files = grep(ref, name)
            if table:
                files &= grep(ref, table)
            if files:
                return where, sorted(files)
        return "", []

    return referenced


def resolve_release(repo: str, release: str, base: str) -> str:
    if release != "auto":
        return release
    tag = git(repo, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", base, check=False).strip()
    if not tag:
        print(f"::warning::migration-lint: no v* release tag is an ancestor of {base[:12]}; checking references against the base")
        return base
    return tag


def added_migrations(repo: str, base: str) -> list[str]:
    tracked = git(repo, "diff", "--name-only", "--diff-filter=A", base, "--", "migrations/*.sql").split()
    untracked = git(repo, "ls-files", "--others", "--exclude-standard", "--", "migrations/*.sql").split()
    return sorted(set(tracked) | set(untracked))


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--repo", default=".")
    parser.add_argument("--base", default=os.environ.get("MIGRATION_LINT_BASE", ""), help="pull request base commit (default: merge-base with origin/main)")
    parser.add_argument("--release", default=os.environ.get("MIGRATION_LINT_RELEASE", "auto"), help="previous release ref; auto = newest v* tag reachable from --base")
    parser.add_argument("files", nargs="*", help="migrations to lint (default: the ones added since --base)")
    args = parser.parse_args(argv)

    repo = args.repo
    try:
        base = args.base or git(repo, "merge-base", "HEAD", "origin/main").strip()
        base = git(repo, "rev-parse", "--verify", f"{base}^{{commit}}").strip()
        files = args.files or added_migrations(repo, base)
        if not files:
            print("migration-lint: OK - this change adds no migrations")
            return 0
        release = resolve_release(repo, args.release, base)
        schema = git(repo, "show", f"{base}:schema.sql", check=False)
        if not schema:
            print(f"::warning::migration-lint: no schema.sql at {base[:12]}; every table is treated as existing")
        catalogue = load_catalogue(schema) if schema else Catalogue(_Everything(), _Everything(), _Everything())
        referenced = make_referenced(repo, release)
    except RuntimeError as err:
        print(f"::error::migration-lint: {err}", file=sys.stderr)
        return 2

    findings: list[Finding] = []
    for path in files:
        with open(os.path.join(repo, path), encoding="utf-8") as fh:
            text = fh.read()
        findings.extend(lint_migration(path, text, catalogue, referenced))
        findings.extend(check_compat_marker(path, text, catalogue))
    for f in findings:
        print(f"::error file={f.path},line={f.line},title=migration-lint {f.rule}::{f.message}")
    if findings:
        print(f"migration-lint: {len(findings)} finding(s) in {len(files)} new migration(s). "
              "See docs/release-compatibility.md (expand/contract); waive with `-- migration-lint: allow <rule> <object>: <reason>` "
              "(compat-marker and the no-downtime rules constraint-validation, constraint-churn, unbatched-write and lock-fanout "
              "cannot be waived).")
        return 1
    print(f"migration-lint: OK - {len(files)} new migration(s) are expand/contract safe against {release}")
    return 0


class _Everything(set):
    """Without a base catalogue every relation counts as existing (strict)."""

    def __contains__(self, item: object) -> bool:
        return True


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
