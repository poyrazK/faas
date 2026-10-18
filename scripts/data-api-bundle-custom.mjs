import assert from 'node:assert/strict'
import { cp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'

// Project-owned input fixtures; all executable CLI/runtime/SDK code comes from the bundle.
export async function prepareCustomProject(app) {
  await cp(new URL('../examples/data-api-orders/',import.meta.url),app,{recursive:true})
  const config=JSON.parse(await readFile(join(app,'data-api.dev.json'),'utf8'))
  config.output='web/database.types.ts'
  config.identities.wrongTenant={subject:'customer-a',claims:{tenant:'shop-b'}}
  config.check={directory:'web',commands:[['npm','run','typecheck']]}
  await writeFile(join(app,'data-api.dev.json'),JSON.stringify(config,null,2)+'\n')
  await writeFile(join(app,'migrations/0001_orders.sql'),`CREATE SCHEMA api;
CREATE TABLE api.orders (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  owner text NOT NULL,
  tenant text NOT NULL,
  total integer NOT NULL CHECK (total >= 0)
);
ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_orders ON api.orders
  USING (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant')
  WITH CHECK (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant');
CREATE FUNCTION api.create_order(order_total integer) RETURNS SETOF api.orders
LANGUAGE sql SECURITY INVOKER SET search_path = pg_catalog AS $$
  INSERT INTO api.orders(owner,tenant,total) VALUES (
    current_setting('request.jwt.claims', true)::jsonb ->> 'sub',
    current_setting('request.jwt.claims', true)::jsonb ->> 'tenant', order_total)
  RETURNING *;
$$;
REVOKE ALL ON FUNCTION api.create_order(integer) FROM PUBLIC;
COMMENT ON FUNCTION api.create_order(integer) IS '@gregale:rpc';
`)
  await writeFile(join(app,'seed.sql'),"INSERT INTO api.orders(owner,tenant,total) VALUES ('customer-a','shop-a',42);\n")
  const collection=JSON.parse(await readFile(join(app,'data-api.requests.json'),'utf8'))
  for(const request of collection.scenarios[0].requests) if(request.body?.owner) request.body.tenant='shop-a'
  collection.scenarios[0].requests.push({name:'Same subject in wrong tenant cannot read',identity:'wrongTenant',method:'GET',path:'/rest/v1/orders',expect:{status:200,json:[]}})
  await writeFile(join(app,'data-api.requests.json'),JSON.stringify(collection,null,2)+'\n')
  const pkg=JSON.parse(await readFile(join(app,'web/package.json'),'utf8'))
  pkg.devDependencies={typescript:'5.9.3'}
  pkg.scripts={typecheck:'tsc --noEmit --strict --target ES2022 --module NodeNext --moduleResolution NodeNext client.ts'}
  await writeFile(join(app,'web/package.json'),JSON.stringify(pkg,null,2)+'\n')
  await writeFile(join(app,'web/client.ts'),`import { createDataClient } from '@gregale/data'
import { schemaFingerprint, type Database } from './database.types.js'
import { schemaFingerprint as olderFingerprint, type Database as Older } from './older.database.types.js'
const current = createDataClient<Database>({ url:'https://orders.example', accessToken:'compile-only', expectedSchemaFingerprint:schemaFingerprint }).schema('api')
const older = createDataClient<Older>({ url:'https://orders.example', accessToken:'compile-only', expectedSchemaFingerprint:olderFingerprint }).schema('api')
current.from('orders').insert({owner:'customer-a',tenant:'shop-a',total:10})
older.from('orders').update({total:20}).eq('id',1)
older.rpc('create_order',{order_total:30}).select('id,total')
current.from('orders').select('id,owner,tenant,total,release_label')
// @ts-expect-error Numeric SQL fields reject string input.
current.from('orders').insert({owner:'customer-a',tenant:'shop-a',total:'invalid'})
// @ts-expect-error Generated identifiers cannot be supplied by older clients.
older.from('orders').insert({id:1,owner:'customer-a',tenant:'shop-a',total:10})
// @ts-expect-error Project-owned RPC arguments retain their schema type.
current.rpc('create_order',{order_total:'invalid'})
`)
}

export const customProfile = {
  table:'orders',valueColumn:'total',identities:['buyer','other'],wrongTenantIdentity:'wrongTenant',
  body:(identity,value)=>({owner:identity==='buyer'?'customer-a':'customer-b',tenant:identity==='buyer'?'shop-a':'shop-b',total:value}),
  values:[101,102,103,104,105,106],rpc:'create_order',rpcArgs:value=>({order_total:value}),rpcExpected:{owner:'customer-a',tenant:'shop-a'},
  migrations:'migrations',client:'web',types:'web/database.types.ts',checkTypes:true,keepOlderTypes:true,
}

export async function verifyCustomContract(app) {
  const contract=JSON.parse(await readFile(join(app,'.gregale/data-api-contract.json'),'utf8'))
  assert.deepEqual(contract.snapshot.tables.map(table=>table.name),['orders'],'Custom release qualification must not depend on notes tables')
  assert.ok(contract.snapshot.functions.some(fn=>fn.name==='create_order'))
}
