import assert from 'node:assert/strict'
import { cp, mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { run } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

export const releaseExamples = Object.freeze(['concurrency','idempotency','outbox','bulk','import-preview','teams'])

export async function verifyPackagedFeatures(client, env) {
  const tests=join(client,'release-sdk-tests')
  await mkdir(tests)
  const files=['cursor','deadline','optimistic','bulk']
  for (const name of files) {
    const source=await readFile(new URL(`../sdk/data/test/${name}.test.mjs`,import.meta.url),'utf8')
    assert.ok(source.includes("'../dist/index.js'"))
    await writeFile(join(tests,`${name}.test.mjs`),source.replaceAll("'../dist/index.js'","'@gregale/data'"))
  }
  run(process.execPath,['--test',...files.map(name=>join(tests,`${name}.test.mjs`))],{cwd:client,env,stdio:'inherit'})
}

export async function verifyPackagedExamples(work, cli, bundleDir, env) {
  const evidence={}
  for (const name of releaseExamples) {
    const app=join(work,`example-${name}`)
    if(name === 'teams') run(cli,['init','--template','data-api-teams','--path',app],{env,stdio:'inherit'})
    else await cp(new URL(`../examples/data-api-${name}/`,import.meta.url),app,{recursive:true})
    if (name === 'teams') {
      run(cli,['data-api','install','--bundle',join(bundleDir,'data-api-bundle.json'),'--directory',app,'--client-directory','client'],{env,stdio:'inherit'})
      run(cli,['data-api','dev','--directory',app,'--port','0','--once','--check-types','--check'],{env,stdio:'inherit',timeout:420000})
      run('npm',['run','build:browser'],{cwd:join(app,'client'),env,stdio:'inherit'})
      const contract=JSON.parse(await readFile(join(app,'.gregale/data-api-contract.json'),'utf8'))
      assert.match(contract.fingerprint,/^[a-f0-9]{64}$/)
      evidence[name]=contract.fingerprint
      continue
    }
    await mkdir(join(app,'web'))
    const config=JSON.parse(await readFile(join(app,'data-api.dev.json'),'utf8'))
    config.output='web/database.types.ts'
    config.check={directory:'web',commands:[['npm','run','typecheck']]}
    await writeFile(join(app,'data-api.dev.json'),JSON.stringify(config,null,2)+'\n')
    await cp(join(app,'client.ts'),join(app,'web/client.ts'))
    await writeFile(join(app,'web/package.json'),JSON.stringify({private:true,type:'module',devDependencies:{typescript:'5.9.3'},scripts:{typecheck:'tsc --noEmit --strict --target ES2022 --module NodeNext --moduleResolution NodeNext client.ts'}}))
    // Install through the shipped owner CLI, then use only that CLI's embedded
    // runtime and the installed SDK. Repository source supplies input fixtures.
    run(cli,['data-api','install','--bundle',join(bundleDir,'data-api-bundle.json'),'--directory',app,'--client-directory','web'],{env,stdio:'inherit'})
    const output=run(cli,['--json','data-api','dev','--port','0','--once','--check'],{cwd:app,env,timeout:420000})
    const contract=JSON.parse(await readFile(join(app,'.gregale/data-api-contract.json'),'utf8'))
    assert.match(contract.fingerprint,/^[a-f0-9]{64}$/)
    assert.match(await readFile(join(app,'web/database.types.ts'),'utf8'),new RegExp(`Schema fingerprint: ${contract.fingerprint}`))
    const receipts=output.split('\n').flatMap(line=>{try{return [JSON.parse(line)]}catch{return []}})
    assert.ok(receipts.some(receipt=>receipt.status==='verified'),'Packaged example must finish verification')
    evidence[name]=contract.fingerprint
    console.log(`Packaged ${name} example types and required scenarios passed.`)
  }
  return evidence
}
