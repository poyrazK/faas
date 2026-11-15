import { readFileSync } from 'node:fs';
import * as z from 'zod/v4';
import { requestJSON } from './openapi-http.js';

export const manifest = JSON.parse(readFileSync(new URL('./openapi-tools.json', import.meta.url)));

export function schemaValidator(schema) {
  let result;
  switch (schema.type) {
    case 'string':
      result = z.string();
      // JSON Schema counts Unicode code points; JS/Zod string lengths count
      // UTF-16 code units. Preserve the contract for non-BMP characters too.
      if (schema.minLength !== undefined) result = result.refine(value => [...value].length >= schema.minLength);
      if (schema.maxLength !== undefined) result = result.refine(value => [...value].length <= schema.maxLength);
      break;
    case 'integer':
    case 'number':
      result = schema.type === 'integer' ? z.number().int() : z.number().finite();
      if (schema.minimum !== undefined) result = result.min(schema.minimum);
      if (schema.maximum !== undefined) result = result.max(schema.maximum);
      break;
    case 'boolean': result = z.boolean(); break;
    case 'null': result = z.null(); break;
    case 'array':
      result = z.array(schemaValidator(schema.items));
      if (schema.minItems !== undefined) result = result.min(schema.minItems);
      if (schema.maxItems !== undefined) result = result.max(schema.maxItems);
      break;
    case 'object': {
      const required = new Set(schema.required || []);
      const fields = Object.fromEntries(Object.entries(schema.properties).map(([name, child]) => {
        const field = schemaValidator(child);
        return [name, required.has(name) ? field : field.optional()];
      }));
      result = schema.additionalProperties === false ? z.strictObject(fields) : z.looseObject(fields);
      break;
    }
    default: throw new Error('Unsupported generated schema');
  }
  if (schema.nullable) result = result.nullable();
  if (schema.enum) {
    const alternatives = schema.enum.map(value => z.literal(value));
    result = z.intersection(result, alternatives.length === 1 ? alternatives[0] : z.union(alternatives));
  }
  const meta = Object.fromEntries(['title', 'description', 'format', 'minLength', 'maxLength']
    .filter(key => schema[key] !== undefined).map(key => [key, schema[key]]));
  return Object.keys(meta).length ? result.meta(meta) : result;
}

export function inputValidator(operation) {
  const fields = {};
  for (const location of ['path', 'query']) {
    const parameters = operation.parameters.filter(p => p.in === location);
    if (!parameters.length) continue;
    const group = z.strictObject(Object.fromEntries(parameters.map(p => {
      const field = schemaValidator(p.schema);
      return [p.name, p.required ? field : field.optional()];
    })));
    fields[location] = parameters.some(p => p.required) ? group : group.optional();
  }
  if (operation.body_schema) {
    const body = schemaValidator(operation.body_schema);
    fields.body = operation.body_required ? body : body.optional();
  }
  return z.strictObject(fields);
}

// Injection is an application-code test seam; published source and fixtures
// still go through the ordinary review, digest and catalog verification.
export function installOpenAPITools(runtime, { request = requestJSON } = {}) {
  for (const name of ['greet', 'add', 'stream_demo', 'build_report']) runtime.removeTool(name);
  for (const name of ['welcome', 'customer_record']) runtime.removeResource(name);
  runtime.removePrompt('summarize');
  for (const operation of manifest.operations) {
    const input = inputValidator(operation);
    const output = z.strictObject({ result: schemaValidator(operation.response_schema) });
    runtime.registerTool(operation.id, {
      description: operation.description, inputSchema: input, outputSchema: output,
      annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: true },
    }, async (arguments_, context) => {
      try {
        const args = input.parse(arguments_);
        const payload = await request(manifest, operation, args, context?.mcpReq?.signal);
        const structuredContent = output.parse({ result: payload });
        return { content: [{ type: 'text', text: 'API operation completed.' }], structuredContent };
      } catch {
        return { isError: true, content: [{ type: 'text', text: 'API operation failed validation, transport or response checks. Reconcile effects before a new attempt.' }] };
      }
    });
  }
}
