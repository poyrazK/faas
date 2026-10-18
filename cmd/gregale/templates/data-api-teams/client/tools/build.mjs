import { build } from 'esbuild'
await build({entryPoints:['src/main.ts'],bundle:true,format:'esm',platform:'browser',target:'es2022',outfile:'client.js'})
