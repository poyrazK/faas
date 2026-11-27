import fs from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';

function html(value) { return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]); }
export default async function merge(input, context) {
  const sources = [];
  let orders = 0n, units = 0n, revenue = 0n;
  for (const name of ['north', 'south']) {
    const step = input?.[name], result = step?.result;
    if (step?.status !== 'succeeded' || result?.currency !== 'USD') throw new Error(`Missing successful ${name} report`);
    for (const field of ['orders', 'units', 'revenue_cents']) {
      if (!Number.isSafeInteger(result[field]) || result[field] < 0) throw new Error(`Invalid ${name} ${field}`);
    }
    const csv = await fs.readFile(path.join(context.input_dir, `${name}.csv`));
    if (!csv.toString('utf8').startsWith('region,orders,units,revenue_cents\n')) throw new Error(`Unexpected ${name} CSV`);
    sources.push({ name, result, sha256: createHash('sha256').update(csv).digest('hex'), csv: csv.toString('utf8') });
    orders += BigInt(result.orders); units += BigInt(result.units); revenue += BigInt(result.revenue_cents);
  }
  if ([orders, units, revenue].some(n => n > BigInt(Number.MAX_SAFE_INTEGER))) throw new Error('Combined report exceeds the safe integer range');
  const result = { currency: 'USD', orders: Number(orders), units: Number(units), revenue_cents: Number(revenue), source_reports: sources.length };
  const metadata = { summary: result, sources: sources.map(({ name, sha256, result }) => ({ name, sha256, result })) };
  await fs.writeFile(path.join(context.output_dir, 'combined.json'), JSON.stringify(metadata, null, 2) + '\n');
  await fs.writeFile(path.join(context.output_dir, 'combined.html'), `<!doctype html><html lang="en"><meta charset="utf-8"><title>Combined sales report</title><h1>Combined sales report</h1><p>${result.orders} orders · ${result.units} units · ${result.revenue_cents} USD cents</p>${sources.map(s => `<section><h2>${html(s.name)}</h2><pre>${html(s.csv)}</pre></section>`).join('')}</html>\n`);
  return result;
}
