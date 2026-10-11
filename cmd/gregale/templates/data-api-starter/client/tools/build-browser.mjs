import { build } from 'esbuild'
await build({ entryPoints: ['browser/main.ts'], bundle: true, format: 'esm', platform: 'browser', target: 'es2022', outfile: 'browser/client.js' })
