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

Tables, views and indexes that do not exist on the base (schema.sql there)
are exempt: nothing old reads them and nothing queues on them.

Only the `-- +goose Up` section is checked, and only migrations this change
adds: issued migrations are append-only history.

A finding is waived by a comment anywhere in the migration naming the rule,
optionally the object, and a reason:

    -- migration-lint: allow index-build events: ~1 s write pause on 394k rows

Usage:
  check_migration_expand_contract.py [--base REF] [--release auto|REF]
  check_migration_expand_contract.py --base REF FILE...   # lint given files
"""
from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from dataclasses import dataclass, field

MAX_LOCK_TIMEOUT_MS = 10_000

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
STATEMENT_STARTERS = {
    "alter", "create", "drop", "lock", "reindex", "cluster", "vacuum", "set",
    "perform", "update", "insert", "delete", "truncate", "select", "comment", "grant",
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
    """Relations and indexes that exist on the base."""

    relations: set[str] = field(default_factory=set)
    indexes: set[str] = field(default_factory=set)


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
                statements.extend(expand(current))
            current = []
            continue
        current.append(tok)
    if current:
        statements.extend(expand(current))
    return statements


def expand(stmt: list[Token]) -> list[list[Token]]:
    if stmt and is_word(stmt[0], "do"):
        bodies = [t for t in stmt if t.kind == "dollar"]
        if bodies:
            inner: list[list[Token]] = []
            for chunk in split_statements(tokenize(bodies[0].value, bodies[0].line)):
                inner.extend(plpgsql_statements(chunk))
            return inner
    return [stmt]


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
class Context:
    path: str
    catalogue: Catalogue
    no_transaction: bool
    referenced: object  # callable(name, table|None) -> (where, files)
    findings: list[Finding] = field(default_factory=list)
    lock_timeout_line: int | None = None
    created: set[str] = field(default_factory=set)
    created_indexes: set[str] = field(default_factory=set)
    defaults_set: set[tuple[str, str]] = field(default_factory=set)
    recreated: set[str] = field(default_factory=set)

    def exists(self, relation: str) -> bool:
        return relation in self.catalogue.relations and relation not in self.created

    def add(self, line: int, rule: str, target: str, message: str) -> None:
        self.findings.append(Finding(self.path, line, rule, target, message))

    def need_lock_timeout(self, line: int, what: str, relation: str) -> None:
        # One finding per relation: the fix is a single SET at the top.
        if self.lock_timeout_line is None and not any(f.rule == "lock-timeout" and f.target == relation for f in self.findings):
            self.add(
                line,
                "lock-timeout",
                relation,
                f"{what} takes a strong lock on existing {relation} with no lock_timeout set before it; "
                f"add `SET LOCAL lock_timeout = '5s';` at the top of the Up section (at most {MAX_LOCK_TIMEOUT_MS // 1000}s).",
            )


def up_section(text: str) -> tuple[str, int]:
    """Return the Up section and the line it starts on."""
    up = re.search(r"^--\s*\+goose\s+Up\b.*$", text, re.MULTILINE | re.IGNORECASE)
    start = up.end() if up else 0
    down = re.search(r"^--\s*\+goose\s+Down\b", text[start:], re.MULTILINE | re.IGNORECASE)
    end = start + down.start() if down else len(text)
    return text[start:end], text.count("\n", 0, start) + 1


def lint_migration(path: str, text: str, catalogue: Catalogue, referenced) -> list[Finding]:
    up, first_line = up_section(text)
    ctx = Context(
        path=path,
        catalogue=catalogue,
        no_transaction=bool(re.search(r"^--\s*\+goose\s+NO\s+TRANSACTION\b", text, re.MULTILINE | re.IGNORECASE)),
        referenced=referenced,
    )
    statements = split_statements(tokenize(up, first_line))
    # Views dropped and created again in the same migration are redefined,
    # not removed.
    for stmt in statements:
        w = words(stmt)
        if w[:2] == ["create", "view"] or w[:4] == ["create", "or", "replace", "view"] or w[:3] == ["create", "materialized", "view"]:
            i = w.index("view") + 1
            i = skip(stmt, i, "if", "not", "exists")
            ctx.recreated.add(name_at(stmt, i)[0])
    for stmt in statements:
        lint_statement(ctx, stmt)
    return apply_waivers(ctx.findings, text)


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
            ctx.lock_timeout_line = line
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
    elif w[0] == "lock":
        relation, _ = name_at(stmt, skip(stmt, 1, "table"))
        if ctx.exists(relation):
            ctx.need_lock_timeout(line, "LOCK", relation)
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
                ctx.need_lock_timeout(line, "DROP INDEX", index)
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
    if len(aw) > 1 and aw[1] in ("constraint", "primary", "unique", "foreign", "check", "exclude"):
        if ("primary" in aw or "unique" in aw) and not has_sequence(aw, "using", "index"):
            ctx.add(line, "index-build", relation,
                    f"ADD PRIMARY KEY/UNIQUE on existing {relation} builds an index under an ACCESS EXCLUSIVE lock. Build it "
                    "with CREATE UNIQUE INDEX CONCURRENTLY, then ADD CONSTRAINT ... USING INDEX.")
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
        if any(rule == f.rule and (target in (None, "*") or target == f.target or f.target.startswith(target + ".")) for rule, target in waivers):
            continue
        kept.append(f)
    return kept


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
    for m in re.finditer(r"^CREATE (?:UNIQUE )?INDEX (\w+) ON ", schema_sql, re.MULTILINE):
        cat.indexes.add(m.group(1))
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
        catalogue = load_catalogue(schema) if schema else Catalogue(relations=_Everything(), indexes=_Everything())
        referenced = make_referenced(repo, release)
    except RuntimeError as err:
        print(f"::error::migration-lint: {err}", file=sys.stderr)
        return 2

    findings: list[Finding] = []
    for path in files:
        with open(os.path.join(repo, path), encoding="utf-8") as fh:
            findings.extend(lint_migration(path, fh.read(), catalogue, referenced))
    for f in findings:
        print(f"::error file={f.path},line={f.line},title=migration-lint {f.rule}::{f.message}")
    if findings:
        print(f"migration-lint: {len(findings)} finding(s) in {len(files)} new migration(s). "
              "See docs/release-compatibility.md (expand/contract); waive with `-- migration-lint: allow <rule> <object>: <reason>`.")
        return 1
    print(f"migration-lint: OK - {len(files)} new migration(s) are expand/contract safe against {release}")
    return 0


class _Everything(set):
    """Without a base catalogue every relation counts as existing (strict)."""

    def __contains__(self, item: object) -> bool:
        return True


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
