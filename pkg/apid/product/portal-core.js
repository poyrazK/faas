(function (root) {
  'use strict';
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  const hash = /^[0-9a-f]{64}$/;
  const fields = new Set(['version', 'scope', 'key', 'fingerprint', 'quote', 'createdAt', 'replayNotAfter', 'operationID']);
  function canonical(value) {
    if (Array.isArray(value)) return value.map(canonical);
    if (value !== null && typeof value === 'object') {
      const out = Object.create(null);
      for (const key of Object.keys(value).sort()) out[key] = canonical(value[key]);
      return out;
    }
    return value;
  }
  const encodeInput = input => JSON.stringify(canonical(input));
  async function fingerprint(crypto, input) {
    const bytes = new TextEncoder().encode(encodeInput(input));
    return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
  }
  class Receipts {
    constructor({ storage, locks, crypto, maxBytes, maxEntries, replaySeconds, now = Date.now }) {
      Object.assign(this, { storage, locks, crypto, maxBytes, maxEntries, replaySeconds, now });
    }
    storageKey(scope) {
      if (!Array.isArray(scope) || scope.length !== 4 || !scope.slice(0, 3).every(v => uuid.test(v)) || !/^[a-z][a-z0-9-]{0,62}$/.test(scope[3])) throw new Error('Invalid customer request scope.');
      return `gregale.product.submission.v1:${JSON.stringify(scope)}`;
    }
    validate(scope, receipt) {
      if (!receipt || Object.keys(receipt).some(k => !fields.has(k)) || receipt.version !== 1 || JSON.stringify(receipt.scope) !== JSON.stringify(scope) ||
        !uuid.test(receipt.key) || !hash.test(receipt.fingerprint) || !hash.test(receipt.quote) ||
        !Number.isSafeInteger(receipt.createdAt) || receipt.createdAt > this.now() || receipt.createdAt <= 0 ||
        receipt.replayNotAfter !== receipt.createdAt + this.replaySeconds * 1000 ||
        (receipt.operationID !== undefined && !uuid.test(receipt.operationID))) throw new Error('Saved request recovery details are invalid. Contact your product team before running again.');
      return receipt;
    }
    load(scope) {
      const raw = this.storage.getItem(this.storageKey(scope));
      if (raw === null) return null;
      if (raw.length > this.maxBytes) throw new Error('Saved request recovery details exceed the supported size.');
      return this.validate(scope, JSON.parse(raw));
    }
    list(identity) {
      const saved = [];
      for (let index = 0; index < this.storage.length; index++) {
        const key = this.storage.key(index);
        if (!key?.startsWith('gregale.product.submission.v1:')) continue;
        let scope; try { scope = JSON.parse(key.slice('gregale.product.submission.v1:'.length)); } catch { continue; }
        if (!Array.isArray(scope) || JSON.stringify(scope.slice(0, 3)) !== JSON.stringify(identity)) continue;
        saved.push(this.load(scope));
        if (saved.length > this.maxEntries) throw new Error('Too many saved request records. Contact your product team to review them before submitting work.');
      }
      return saved;
    }
    save(scope, receipt) {
      this.validate(scope, receipt);
      const raw = JSON.stringify(receipt), key = this.storageKey(scope);
      if (raw.length > this.maxBytes) throw new Error('Request recovery details exceed the supported size.');
      if (this.storage.getItem(key) === null && this.list(scope.slice(0, 3)).length >= this.maxEntries) throw new Error('Too many saved request records. Contact your product team before submitting work.');
      this.storage.setItem(key, raw);
      if (this.storage.getItem(key) !== raw) throw new Error('Your browser could not save request recovery details. Work was not submitted.');
    }
    lock(scope, work) {
      if (!this.locks || typeof this.locks.request !== 'function') throw new Error('Use a supported browser with site storage enabled to submit and recover your requests.');
      return this.locks.request(this.storageKey(scope), work);
    }
    async prepare(scope, input, quote) {
      let receipt = this.load(scope);
      const digest = await fingerprint(this.crypto, input);
      if (receipt && !receipt.operationID) {
        if (this.now() >= receipt.replayNotAfter) throw new Error('This request is past its safe retry window. Contact your product team to resolve it before starting again.');
        if (receipt.fingerprint !== digest) throw new Error('Enter the original request details to retry this saved request.');
        return receipt;
      }
      const createdAt = this.now();
      receipt = { version: 1, scope: [...scope], key: this.crypto.randomUUID(), fingerprint: digest, quote, createdAt, replayNotAfter: createdAt + this.replaySeconds * 1000 };
      this.save(scope, receipt);
      return receipt;
    }
    acknowledge(scope, receipt, operationID) {
      const current = this.load(scope);
      if (!current || current.key !== receipt.key) throw new Error('The saved request changed in another tab. Refresh before continuing.');
      const accepted = { ...current, operationID };
      this.save(scope, accepted);
      return accepted;
    }
  }
  const exported = Object.freeze({ Receipts, encodeInput, fingerprint });
  root.GregaleProduct = exported;
  if (typeof module !== 'undefined') module.exports = exported;
})(globalThis);
