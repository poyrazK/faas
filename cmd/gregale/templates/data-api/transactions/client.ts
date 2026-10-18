import { validatedRpc } from '@gregale/data'
import { rpcValidators, type Database } from './database.types.js'

type Args = Database['api']['Functions']['__OP__']['Args']
export const transactionArguments = {
 parse(input: unknown): Args {
  // Generated checks own argument names, presence and shapes; the remaining
  // checks are this example's business rules, mirrored in the SQL function.
  const value = rpcValidators.api.__OP__.parse(input)
  if (typeof value.request_key !== 'string' || !/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value.request_key)
   || !Array.isArray(value.skus) || !Array.isArray(value.quantities) || value.skus.length < 1 || value.skus.length > 100 || value.skus.length !== value.quantities.length
   || value.skus.some(sku => typeof sku !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(sku)) || new Set(value.skus).size !== value.skus.length
   || value.quantities.some(quantity => typeof quantity !== 'number' || !Number.isSafeInteger(quantity) || quantity < 1 || quantity > 1000)) throw new Error('Invalid transaction arguments')
  return { request_key: value.request_key, skus: [...value.skus], quantities: [...value.quantities] } as Args
 },
}
// Import the generated Database into your existing createDataClient. Keep the
// same request_key across uncertain network outcomes; change it for a new order.
// const outcome = await validatedRpc(input, transactionArguments,
//   args => db.schema('api').rpc('__OP__', args))
// The callback retains schema-generated result inference. Validation failures
// omit input/parser exceptions; server errors remain in outcome.result.
export { validatedRpc }
