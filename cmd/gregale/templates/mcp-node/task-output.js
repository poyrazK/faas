import { AjvJsonSchemaValidator } from '@modelcontextprotocol/server/validators/ajv';

function compile(schema) {
  try {
    if (!schema || typeof schema !== 'object' || Array.isArray(schema)) throw new Error();
    // A fresh provider prevents a reused $id from selecting another task's
    // schema. No remote loader, coercion, or default insertion is configured.
    return new AjvJsonSchemaValidator().getValidator(schema);
  } catch {
    throw new Error('Invalid MCP Task output schema');
  }
}

export function snapshotTaskOutputSchema(schema) {
  if (schema === undefined) return undefined;
  try {
    if (schema?.['~standard']?.jsonSchema) schema = schema['~standard'].jsonSchema.output({ target: 'draft-2020-12' });
    const snapshot = JSON.parse(JSON.stringify(schema));
    compile(snapshot);
    return snapshot;
  } catch {
    throw new Error('Invalid MCP Task output schema');
  }
}

export function validateTaskOutput(result, schema) {
  // Tasks admitted before this expansion have no saved contract.
  if (schema == null) return;
  if (!result || typeof result !== 'object' || Array.isArray(result) || !Array.isArray(result.content)
    || result.resultType !== undefined && result.resultType !== 'complete') {
    throw new Error('Invalid MCP Task tool result');
  }
  if (result.isError === true) return;
  if (!result.structuredContent || typeof result.structuredContent !== 'object' || Array.isArray(result.structuredContent)
    || !compile(schema)(result.structuredContent).valid) {
    throw new Error('MCP Task output does not match outputSchema');
  }
}
