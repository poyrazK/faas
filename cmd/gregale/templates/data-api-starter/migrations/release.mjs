import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { migrate } from './migrate.mjs'
import { permissions, options } from './rpc-permissions.mjs'

// Migrations commit first; permission failure blocks deployment readiness but
// cannot undo committed schema changes. Sync must wait for the release task.
export async function release(connectionString, { role } = {}) {
  await migrate(connectionString)
  const report = await permissions(connectionString, { role, apply: true })
  if (!report.ready) throw new Error('RPC permissions are not ready')
  return report
}

const isMain = process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)
if (isMain && !process.env.MIGRATION_DATABASE_URL) {
  console.error('Migration release failed: MIGRATION_DATABASE_URL is not set. Attach the managed database first: gregale postgres attach <database> <app> --access migration --env MIGRATION_DATABASE_URL')
  process.exitCode = 1
} else if (isMain) {
  try {
    const report = await release(process.env.MIGRATION_DATABASE_URL, options(process.argv.slice(2)))
    console.log(JSON.stringify(report))
  } catch (error) {
    if (error.report) console.log(JSON.stringify(error.report))
    console.error('Migration release failed: check migrations, owner binding, RPC eligibility and role selection. Committed migrations are retained.')
    process.exitCode = 1
  }
}
