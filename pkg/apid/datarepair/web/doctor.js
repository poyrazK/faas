'use strict';
class RepairDoctor {
  constructor({request, locked, scope}) {
    this.request = request; this.locked = locked; this.scope = scope;
    this.el = id => document.getElementById(id);
    this.busy = false; this.generation = 0;
    this.el('doctor-form').onsubmit = event => { event.preventDefault(); this.load(); };
    this.el('doctor-table').oninput = () => this.reset();
    this.controls();
  }
  controls() {
    for (const input of this.el('doctor-form').elements) input.disabled = this.busy || this.locked();
  }
  reset() {
    this.generation++; this.busy = false;
    this.el('doctor-results').replaceChildren(); this.el('doctor-message').textContent = '';
    this.controls();
  }
  async load() {
    if (this.busy || this.locked()) return;
    const table = this.el('doctor-table').value.trim(), scope = this.scope?.();
    if (!table) return;
    if (this.scope && !scope) { this.reset(); this.el('doctor-message').textContent = 'Choose a configured environment and customer above.'; return; }
    const params = new URLSearchParams({table});
    if (scope) { params.set('environment', scope.environment); params.set('customer', scope.platform_tenant_id || ''); }
    const generation = ++this.generation;
    this.busy = true; this.controls(); this.el('doctor-results').replaceChildren();
    this.el('doctor-message').textContent = 'Inspecting repair configuration…';
    try {
      const report = await this.request(params);
      if (generation !== this.generation) return;
      const required = ['connection', 'postgres', 'installation', 'functions', 'enrollment', 'fingerprint', 'triggers', 'permissions', 'policy'];
      if (report.version !== 1 || report.profile && report.profile !== 'executor' || report.table !== table || !report.scope || !Array.isArray(report.findings) ||
        !['ready', 'blocked', 'unknown'].includes(report.status) || !report.checked_at || !report.policy ||
        (scope && (report.scope.environment !== scope.environment || (report.scope.platform_tenant_id || '') !== (scope.platform_tenant_id || ''))) ||
        report.findings.length !== required.length + (scope ? 1 : 0) || new Set(report.findings.map(f => f.id)).size !== report.findings.length ||
        required.some(id => !report.findings.some(f => f.id === id)) || (scope && !report.findings.some(f => f.id === 'binding')) ||
        report.findings.some(f => !['ready', 'blocked', 'unknown'].includes(f.status) || !Array.isArray(f.objects)) ||
        report.status !== (report.findings.some(f => f.status === 'blocked') ? 'blocked' : report.findings.some(f => f.status === 'unknown') ? 'unknown' : 'ready')) throw new Error('Readiness did not match the selected table and scope.');
      this.el('doctor-results').replaceChildren(...report.findings.map(f => {
        const row = document.createElement('tr');
        for (const value of [f.id, f.status, f.summary, f.objects.join('\n'), f.action]) {
          const cell = document.createElement('td'); cell.textContent = value; row.append(cell);
        }
        return row;
      }));
      this.el('doctor-message').textContent = `Configuration ${report.status} as of ${report.checked_at}. Policy ${report.policy.mode}${report.policy.digest ? ' · SHA-256 ' + report.policy.digest : ''}. This covers the connected executor role. Check application writers separately with the SDK capture readiness helper or the local capture profile. Capture coverage, business-check results and rehearsal remain separate checks.`;
    } catch (error) {
      if (generation === this.generation) { this.el('doctor-results').replaceChildren(); this.el('doctor-message').textContent = `Readiness unknown. ${error.message}`; }
    } finally {
      if (generation === this.generation) { this.busy = false; this.controls(); }
    }
  }
}
