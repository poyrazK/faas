import { closeSync, existsSync, fsyncSync, mkdirSync, openSync, readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { randomUUID } from "node:crypto";
import { conflict, defaultOrder, fingerprint, normalizeOrder } from "./contract.js";

// A single-writer demo ledger. Replace this with transactional storage when
// running multiple application instances against the same business ledger.
export function openLedger(path) {
  const entries = new Map();
  let id = randomUUID();
  if (path && existsSync(path)) {
    const document = JSON.parse(readFileSync(path, "utf8"));
    if (document.version !== 3 || typeof document.id !== "string" || !/^[0-9a-f-]{36}$/.test(document.id) || !Array.isArray(document.entries)) throw new Error("invalid ledger");
    id = document.id;
    for (const entry of document.entries) {
      if (!Array.isArray(entry) || entry.length !== 2 || typeof entry[0] !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(entry[0]) || entries.has(entry[0]) || !entry[1] || !["pending", "confirmed"].includes(entry[1].state)) {
        throw new Error("invalid ledger entry");
      }
      if (fingerprint(entry[1].order) !== entry[1].fingerprint) throw new Error("invalid ledger fingerprint");
      entries.set(...entry);
    }
  }
  let sequence = 0;
  return {
    id,
    get: key => entries.has(key) ? structuredClone(entries.get(key)) : undefined,
    count: state => [...entries.values()].filter(value => value.state === state).length,
    set(key, state, order = defaultOrder) {
      if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key) || !["pending", "confirmed"].includes(state)) throw new Error("invalid ledger entry");
      order = normalizeOrder(order);
      const hash = fingerprint(order);
      const existing = entries.get(key);
      // This check and commit are synchronous in the single ledger writer.
      if (existing && existing.fingerprint !== hash) throw conflict();
      if (existing?.state === "confirmed" || existing?.state === state) return structuredClone(existing);
      const next = new Map(entries);
      const record = { state, fingerprint: hash, order };
      next.set(key, record);
      if (path) {
        mkdirSync(dirname(path), { recursive: true });
        const temporary = `${path}.${process.pid}.${sequence++}.tmp`;
        let fd;
        try {
          fd = openSync(temporary, "wx", 0o600);
          writeFileSync(fd, JSON.stringify({ version: 3, id, entries: [...next] }));
          fsyncSync(fd);
          closeSync(fd);
          fd = undefined;
          renameSync(temporary, path);
          const directory = openSync(dirname(path), "r");
          try { fsyncSync(directory); } finally { closeSync(directory); }
        } finally {
          if (fd !== undefined) closeSync(fd);
          if (existsSync(temporary)) unlinkSync(temporary);
        }
      }
      entries.clear();
      for (const [key, value] of next) entries.set(key, value);
      return structuredClone(record);
    },
  };
}
