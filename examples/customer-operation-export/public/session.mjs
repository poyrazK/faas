// Browser state is disposable. The authenticated history API owns discovery.
export class ExportSession {
  constructor({client, appID, scope, definitionID, name = 'customer-export', onChange = () => {}}) {
    Object.assign(this, {client, appID, scope, definitionID, name, onChange});
    this.lifetime = new AbortController(); this.rows = []; this.cursor = undefined;
  }
  async history({more = false} = {}) {
    if (more && !this.cursor) return this.rows;
    const epoch = this.historyEpoch = (this.historyEpoch ?? 0) + 1;
    const page = await this.client.list({appID: this.appID, scope: this.scope, name: this.name, ...(more && this.cursor ? {cursor: this.cursor} : {})}, this.lifetime.signal);
    if (this.lifetime.signal.aborted || epoch !== this.historyEpoch) return;
    this.rows = more ? [...this.rows, ...page.operations] : page.operations;
    this.cursor = page.next_cursor; this.onChange({history: this.rows, hasMore: Boolean(this.cursor)});
    return this.rows;
  }
  start(count) {
    if (!Number.isInteger(count) || count < 1 || count > 1000) return Promise.reject(new Error('Choose 1 to 1000 rows'));
    if (this.pending && this.pending.count !== count) return Promise.reject(new Error('Retry the pending submission with the same input first'));
    if (this.starting) return this.starting;
    this.pending ??= {count, key: crypto.randomUUID()};
    this.starting = this.submit().finally(() => { this.starting = undefined; });
    return this.starting;
  }
  async submit() {
    const receipt = await this.client.start(this.definitionID, {count: this.pending.count}, this.pending.key, this.lifetime.signal);
    this.pending = undefined;
    try { await this.open(receipt.id); await this.history(); }
    catch (error) {
      if (this.lifetime.signal.aborted) throw error;
      // Acceptance is durable even if the following status read fails.
      this.onChange({error: `Export accepted (${receipt.id}). Reopen it from history: ${error.message}`});
    }
    return receipt;
  }
  async open(id) {
	if (this.lifetime.signal.aborted) throw new DOMException('Session closed', 'AbortError');
    this.watch?.abort(); const watch = this.watch = new AbortController(); this.selected = id;
    const snapshot = await this.client.get(id, watch.signal);
    if (watch.signal.aborted) return;
    this.apply(snapshot);
    // Watch owns its errors and never reruns business work on a stream failure.
    this.watching = this.observe(id, watch).catch(error => {
      if (!watch.signal.aborted) this.onChange({error: error.message});
    });
  }
  apply(snapshot) { this.snapshot = snapshot; this.onChange({operation: snapshot}); }
  async observe(id, watch) {
    for await (const update of this.client.subscribe(id, {after: this.snapshot.latest_sequence, signal: watch.signal})) {
      const snapshot = update.snapshot ?? await this.client.get(id, watch.signal);
      if (watch.signal.aborted) return;
      this.apply(snapshot);
    }
  }
  async refresh() {
    await this.history();
    if (this.selected) {
      const snapshot = await this.client.get(this.selected, this.lifetime.signal);
      if (!this.lifetime.signal.aborted && snapshot.id === this.selected) this.apply(snapshot);
    }
  }
  async cancel() {
    if (!this.snapshot) return;
    const snapshot = await this.client.cancel(this.snapshot.id, this.snapshot.generation, this.lifetime.signal);
    if (!this.lifetime.signal.aborted && snapshot.id === this.selected) this.apply(snapshot);
  }
  async download() {
    if (!this.selected) throw new Error('Select an export first');
    const selected = this.selected;
    const current = () => { if (this.lifetime.signal.aborted || selected !== this.selected) throw new DOMException('Session changed', 'AbortError'); };
    const snapshot = await this.client.get(selected, this.lifetime.signal); current();
    if (snapshot.state !== 'succeeded') throw new Error('The export is not confirmed complete');
    if (snapshot.artifacts?.length) {
      const blob = await (await this.client.download(snapshot.id, snapshot.artifacts[0].id, this.lifetime.signal)).blob(); current();
      return {name: snapshot.artifacts[0].name, blob};
    }
    if (typeof snapshot.result?.csv !== 'string') throw new Error('The retained export is unavailable');
    return {name: 'export.csv', blob: new Blob([snapshot.result.csv], {type: 'text/csv'})};
  }
  close() { this.lifetime.abort(); this.watch?.abort(); this.selected = undefined; this.rows = []; this.snapshot = undefined; this.pending = undefined; }
}
