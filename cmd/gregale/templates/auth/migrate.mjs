import { spawnSync } from 'node:child_process'
import { gotrue, migrationEnv, runtimeConfig } from './config.mjs'

// Release task: apply GoTrue's pinned migrations (with the Gregale overlay)
// through the auth binding, which acts as the auth schemas' owner (ADR-1190).
// Serving instances never migrate, so snapshot restores perform no DDL.
const config = runtimeConfig(process.env)
const result = spawnSync(gotrue.binary, ['migrate'], { env: migrationEnv(config), stdio: 'inherit' })
if (result.error) {
  console.error(JSON.stringify({ level: 'error', event: 'gotrue_migrate_failed', error: result.error.code ?? 'spawn_failed' }))
  process.exit(1)
}
process.exit(result.status ?? 1)
