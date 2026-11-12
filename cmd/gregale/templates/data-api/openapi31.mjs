// Convert the filtered, enriched Swagger contract. No catalog reads or owner
// SQL definitions enter this public document.
export function openAPI31(swagger) {
  const rewriteRef = ref => ref.replace(/^#\/definitions\//, '#/components/schemas/').replace(/^#\/parameters\//, '#/components/parameters/').replace(/^#\/responses\//, '#/components/responses/')
  function schema(value) {
    if (!value || typeof value !== 'object') return value
    if (Array.isArray(value)) return value.map(schema)
    const result = {}
    for (const [key, item] of Object.entries(value)) {
      if (key === 'x-nullable' || key === 'x-gregale-bulk-schema') continue
      result[key] = key === '$ref' ? rewriteRef(item) : schema(item)
    }
    if (result.type === 'file') { result.type = 'string'; result.format = 'binary' }
    if (value['x-gregale-bulk-schema']) return { anyOf: [schema({ ...value, 'x-gregale-bulk-schema': undefined }), schema(value['x-gregale-bulk-schema'])] }
    if (value['x-nullable']) {
      if (result.enum && !result.enum.includes(null)) result.enum.push(null)
      if (typeof result.type === 'string') result.type = [result.type, 'null']
      else if (result.$ref || result.allOf || result.anyOf || result.oneOf) return { anyOf: [result, { type: 'null' }] }
    }
    return result
  }
  const resolveParameter = parameter => parameter.$ref?.startsWith('#/parameters/') ? swagger.parameters?.[parameter.$ref.slice(13).replaceAll('~1', '/').replaceAll('~0', '~')] : parameter
  function parameter(value) {
    if (value.$ref) return { $ref: rewriteRef(value.$ref) }
    const { name, in: location, required, description, collectionFormat, ...shape } = value
    return { name, in: location, ...(required !== undefined ? { required } : {}), ...(description ? { description } : {}),
      schema: schema(shape.schema ?? shape), ...(collectionFormat ? { style: location === 'query' ? 'form' : 'simple', explode: collectionFormat === 'multi' } : {}) }
  }
  function response(value, produces) {
    if (value.$ref) return { $ref: rewriteRef(value.$ref) }
    const { schema: shape, headers, examples, ...rest } = value
    const representation = media => /^text\//.test(media) ? { type: 'string' } : media.includes('vnd.pgrst.object') && shape.type === 'array' ? schema(shape.items) : schema(shape)
    return { ...rest, ...(headers ? { headers: Object.fromEntries(Object.entries(headers).map(([name, header]) => {
      const { description, ...type } = header
      return [name, { ...(description ? { description } : {}), schema: schema(type) }]
    })) } : {}), ...(shape ? { content: Object.fromEntries(produces.map(media => [media, { schema: representation(media), ...(examples?.[media] ? { example: examples[media] } : {}) }])) } : {}) }
  }
  const paths = {}
  for (const [path, item] of Object.entries(swagger.paths ?? {})) {
    paths[path] = {}
    if (item.parameters) paths[path].parameters = item.parameters.filter(p => resolveParameter(p)?.in !== 'body').map(parameter)
    for (const method of ['get', 'post', 'put', 'patch', 'delete', 'head', 'options']) {
      if (!item[method]) continue
      const { parameters = [], responses = {}, consumes, produces, ...rest } = item[method]
      const body = [...(item.parameters ?? []), ...parameters].map(resolveParameter).find(p => p?.in === 'body')
      paths[path][method] = { ...rest, parameters: parameters.filter(p => resolveParameter(p)?.in !== 'body').map(parameter),
        ...(body ? { requestBody: { required: body.required === true, ...(body.description ? { description: body.description } : {}), content: Object.fromEntries((consumes ?? swagger.consumes ?? ['application/json']).map(media => [media, { schema: schema(body.schema ?? {}) }])) } } : {}),
        responses: Object.fromEntries(Object.entries(responses).map(([status, value]) => [status, response(value, produces ?? swagger.produces ?? ['application/json'])])) }
    }
  }
  const result = { openapi: '3.1.0', jsonSchemaDialect: 'https://spec.openapis.org/oas/3.1/dialect/base', info: swagger.info ?? { title: 'Gregale Data API', version: '1' }, servers: [{ url: swagger.basePath || '/rest/v1' }], paths,
    components: { schemas: Object.fromEntries(Object.entries(swagger.definitions ?? {}).map(([name, value]) => [name, schema(value)])),
      parameters: Object.fromEntries(Object.entries(swagger.parameters ?? {}).filter(([, value]) => value.in !== 'body').map(([name, value]) => [name, parameter(value)])),
      responses: Object.fromEntries(Object.entries(swagger.responses ?? {}).map(([name, value]) => [name, response(value, swagger.produces ?? ['application/json'])])),
      securitySchemes: { bearer: { type: 'http', scheme: 'bearer', bearerFormat: 'JWT' } } }, security: swagger.security ?? [{ bearer: [] }] }
  for (const [key, value] of Object.entries(swagger)) if (key.startsWith('x-') || ['tags', 'externalDocs'].includes(key)) result[key] = value
  return result
}
