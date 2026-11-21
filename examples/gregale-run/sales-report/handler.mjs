import fs from 'node:fs/promises';
import path from 'node:path';

// One file, built-in modules only: the same handler can be submitted as source.
export function parseCSV(text) {
  const rows = []; let row = [], cell = '', quoted = false, closed = false;
  text = text.replace(/^\uFEFF/, '');
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') { cell += '"'; i++; }
      else if (c === '"') { quoted = false; closed = true; }
      else cell += c;
    } else if (c === '"') {
      if (cell || closed) throw new Error('Unexpected quote in CSV');
      quoted = true;
    } else if (c === ',' || c === '\n' || c === '\r') {
      row.push(cell); cell = ''; closed = false;
      if (c !== ',') { rows.push(row); row = []; if (c === '\r' && text[i + 1] === '\n') i++; }
    } else {
      if (closed) throw new Error('Unexpected data after CSV quote');
      cell += c;
    }
  }
  if (quoted) throw new Error('Unclosed CSV quote');
  if (cell || closed || row.length) { row.push(cell); rows.push(row); }
  return rows;
}

function integer(value, name, minimum) {
  if (typeof value === 'string' && /^\d+$/.test(value)) value = Number(value);
  if (!Number.isSafeInteger(value) || value < minimum) throw new Error(`Invalid ${name}`);
  return value;
}
function label(value, name) {
  if (typeof value !== 'string' || !value.trim() || value.length > 96) throw new Error(`Invalid ${name}`);
  return value;
}
function cell(value) {
  let text = String(value);
  if (/^[=+\-@\t\r\n]/.test(text)) text = "'" + text;
  return `"${text.replaceAll('"', '""')}"`;
}
function html(value) { return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]); }
function checked(value) {
  if (value > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Report exceeds the safe integer range');
  return Number(value);
}

export default async function report(input, context) {
  if (!input || (input.rows === undefined) === (input.csv_file === undefined)) throw new Error('Provide rows or csv_file, exclusively');
  let rows = input.rows;
  if (input.csv_file !== undefined) {
    const name = input.csv_file;
    if (typeof name !== 'string' || !name || path.isAbsolute(name) || name.includes('\\') || name.split('/').some(p => !p || p === '.' || p === '..')) throw new Error('Invalid csv_file path');
    const parsed = parseCSV(await fs.readFile(path.join(context.input_dir, name), 'utf8'));
    const header = ['order_id', 'region', 'quantity', 'unit_price_cents'];
    if (JSON.stringify(parsed.shift()) !== JSON.stringify(header)) throw new Error(`CSV header must be ${header.join(',')}`);
    rows = parsed.map((r, i) => {
      if (r.length !== header.length) throw new Error(`Wrong column count on record ${i + 2}`);
      return Object.fromEntries(header.map((name, j) => [name, r[j]]));
    });
  }
  if (!Array.isArray(rows) || rows.length < 1 || rows.length > 10000) throw new Error('Expected 1..10000 orders');
  const ids = new Set(), regions = new Map(); let units = 0n, revenue = 0n;
  for (const order of rows) {
    if (!order || typeof order !== 'object') throw new Error('Invalid order');
    const id = label(order.order_id, 'order_id'), region = label(order.region, 'region');
    if (ids.has(id)) throw new Error('Duplicate order_id');
    ids.add(id);
    const quantity = integer(order.quantity, 'quantity', 1), price = integer(order.unit_price_cents, 'unit_price_cents', 0);
    const amount = BigInt(quantity) * BigInt(price);
    units += BigInt(quantity); revenue += amount;
    const aggregate = regions.get(region) ?? { orders: 0, units: 0n, revenue: 0n };
    aggregate.orders++; aggregate.units += BigInt(quantity); aggregate.revenue += amount; regions.set(region, aggregate);
  }
  const result = {
    currency: 'USD', orders: rows.length, units: checked(units), revenue_cents: checked(revenue),
    regions: [...regions].sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([region, r]) => ({ region, orders: r.orders, units: checked(r.units), revenue_cents: checked(r.revenue) })),
  };
  const csv = ['region,orders,units,revenue_cents', ...result.regions.map(r => [cell(r.region), r.orders, r.units, r.revenue_cents].join(','))].join('\n') + '\n';
  const document = `<!doctype html><html lang="en"><meta charset="utf-8"><title>Sales report</title><h1>Sales report</h1><p>${result.orders} orders · ${result.units} units · ${result.revenue_cents} USD cents</p><table><thead><tr><th>Region</th><th>Orders</th><th>Units</th><th>Revenue (USD cents)</th></tr></thead><tbody>${result.regions.map(r => `<tr><td>${html(r.region)}</td><td>${r.orders}</td><td>${r.units}</td><td>${r.revenue_cents}</td></tr>`).join('')}</tbody></table></html>\n`;
  await fs.writeFile(path.join(context.output_dir, 'summary.csv'), csv);
  await fs.writeFile(path.join(context.output_dir, 'report.html'), document);
  return result;
}
