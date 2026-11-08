import { createHash } from 'node:crypto';

const NAME_PATTERN = /^[\x21-\x7e]{1,128}$/;
const RESOURCE_VARIABLE_PATTERN = /\{([A-Za-z][A-Za-z0-9_.-]*)\}/g;

// Keep the Standard Schema validator, but snapshot its JSON representations.
// Zod's immutable structural APIs still retain mutable nested .meta() values.
function snapshotSchema(value) {
  const standard = value['~standard'];
  if (typeof standard?.validate !== 'function' || !standard.jsonSchema) return undefined;
  const jsonSchema = {};
  for (const io of ['input', 'output']) {
    let converted;
    try { converted = standard.jsonSchema[io]({ target: 'draft-2020-12' }); }
    catch (error) {
      // Some validators support conversion in only one direction (transforms,
      // for example). Preserve that error for consumers of that direction.
      jsonSchema[io] = () => { throw error; };
      continue;
    }
    // Capture the actual wire JSON, including values with toJSON (such as Date
    // examples). Zod's non-enumerable ~standard decoration is excluded too.
    const snapshot = JSON.parse(JSON.stringify(converted));
    jsonSchema[io] = () => snapshotMetadata(snapshot, new Set(), false);
  }
  const result = { '~standard': Object.freeze({
    version: standard.version, vendor: standard.vendor,
    validate: standard.validate.bind(standard), jsonSchema: Object.freeze(jsonSchema),
  }) };
  // Preserve these convenient validation methods for catalog readers. The SDK
  // uses ~standard.validate, including its asynchronous validation semantics.
  for (const method of ['safeParse', 'safeParseAsync', 'parse', 'parseAsync']) {
    if (typeof value[method] === 'function') result[method] = value[method].bind(value);
  }
  // The SDK inspects Zod object fields for prompt completions. Retain only the
  // callbacks it needs, without exposing the original field schemas/metadata.
  if (value.shape && typeof value.shape === 'object') {
    const symbol = Symbol.for('mcp.completable');
    result.shape = Object.freeze(Object.fromEntries(Object.entries(value.shape).map(([name, field]) => {
      const inner = field.type === 'optional' ? field.def?.innerType ?? field : field;
      const complete = inner[symbol]?.complete;
      return [name, Object.freeze(typeof complete === 'function' ? { [symbol]: Object.freeze({ complete }) } : {})];
    })));
  }
  return Object.freeze(result);
}

// Copy plain metadata and JSON schemas so application and SDK consumers own no
// mutable metadata references into the stored catalog.
function snapshotMetadata(value, parents = new Set(), adaptSchemas = true) {
  if (value === null || typeof value !== 'object') return value;
  const schema = adaptSchemas && snapshotSchema(value);
  if (schema) return schema;
  const prototype = Object.getPrototypeOf(value);
  if (!Array.isArray(value) && prototype !== Object.prototype && prototype !== null) return value;
  if (parents.has(value)) throw new TypeError('MCP catalog metadata must not contain cycles');
  parents.add(value);
  const result = Array.isArray(value) ? new Array(value.length) : Object.create(prototype);
  for (const [key, item] of Object.entries(value)) {
    Object.defineProperty(result, key, { value: snapshotMetadata(item, parents, adaptSchemas), enumerable: true, writable: true, configurable: true });
  }
  parents.delete(value);
  return result;
}

function snapshotEntry(entry) {
  return { ...entry, definition: snapshotMetadata(entry.definition) };
}

function canonicalJSON(value) {
  if (value === null || typeof value !== 'object') {
    if (typeof value === 'function' || typeof value === 'bigint' || value === undefined) {
      throw new TypeError('MCP catalog definitions must contain only JSON values');
    }
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
  const keys = Object.keys(value).filter(key => value[key] !== undefined).sort();
  return `{${keys.map(key => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`;
}

function resourcePattern(uriOrTemplate) {
  if (typeof uriOrTemplate === 'string') return uriOrTemplate;
  const template = uriOrTemplate?.uriTemplate?.toString();
  return typeof template === 'string' ? template : '';
}

function validAbsoluteURI(uri) {
  if (typeof uri !== 'string' || uri.length === 0 || Buffer.byteLength(uri) > 16 * 1024 || /[\x00-\x1f\x7f]/.test(uri)) return false;
  try {
    return Boolean(new URL(uri).protocol);
  } catch {
    return false;
  }
}

function validateResource(name, uriOrTemplate, definition, handler) {
  if (!NAME_PATTERN.test(name)) throw new TypeError('MCP resource name must be 1 to 128 visible ASCII characters without whitespace');
  if (!definition || typeof definition !== 'object' || Array.isArray(definition)) throw new TypeError('MCP resource definition must be an object');
  if (typeof handler !== 'function') throw new TypeError('MCP resource handler is required');
  const pattern = resourcePattern(uriOrTemplate);
  if (!pattern) throw new TypeError('MCP resource URI or URI template is required');
  const expanded = pattern.replace(RESOURCE_VARIABLE_PATTERN, 'value');
  RESOURCE_VARIABLE_PATTERN.lastIndex = 0;
  if (/[{}]/.test(expanded) || !validAbsoluteURI(expanded)) throw new TypeError('MCP resource URI template must be an absolute URI');
  return pattern;
}

export function createRuntimeCatalog({ normalizeDefinition = definition => definition } = {}) {
  const entries = { tools: new Map(), resources: new Map(), prompts: new Map() };
  const listeners = new Set();
  let revision = 0;

  function requireDefinition(name, definition, handler) {
    if (typeof name !== 'string' || !NAME_PATTERN.test(name)) throw new TypeError('MCP catalog name must be 1 to 128 visible ASCII characters without whitespace');
    if (!definition || typeof definition !== 'object' || Array.isArray(definition)) throw new TypeError('MCP catalog definition must be an object');
    if (typeof handler !== 'function') throw new TypeError('MCP catalog handler is required');
  }

  function add(kind, name, entry, replace = false) {
    const catalog = entries[kind];
    if (!catalog) throw new TypeError('unknown MCP catalog kind');
    if (!replace && catalog.has(name)) throw new Error(`MCP ${kind.slice(0, -1)} ${name} is already registered`);
    if (replace && !catalog.has(name)) throw new Error(`MCP ${kind.slice(0, -1)} ${name} is not registered`);
    if (kind === 'resources') {
      for (const [otherName, existing] of catalog) {
        if (otherName !== name && existing.policyKey === entry.policyKey) throw new Error(`MCP resource URI ${entry.policyKey} is already registered`);
      }
    }
    const stored = snapshotEntry(entry);
    revision += 1;
    catalog.set(name, { ...stored, name, revision });
    for (const listener of [...listeners]) {
      try { listener(kind, revision); } catch { /* A listener must not block a catalog mutation. */ }
    }
  }

  function remove(kind, name) {
    const catalog = entries[kind];
    if (!catalog || !catalog.delete(name)) return false;
    revision += 1;
    for (const listener of [...listeners]) {
      try { listener(kind, revision); } catch { /* A listener must not block a catalog mutation. */ }
    }
    return true;
  }

  function registerTool(name, definition, handler) {
    requireDefinition(name, definition, handler);
    add('tools', name, { definition, handler });
  }

  function replaceTool(name, definition, handler) {
    requireDefinition(name, definition, handler);
    add('tools', name, { definition, handler }, true);
  }

  function registerResource(name, uriOrTemplate, definition, handler) {
    const policyKey = validateResource(name, uriOrTemplate, definition, handler);
    add('resources', name, { uriOrTemplate, policyKey, definition, handler });
  }

  function replaceResource(name, uriOrTemplate, definition, handler) {
    const policyKey = validateResource(name, uriOrTemplate, definition, handler);
    add('resources', name, { uriOrTemplate, policyKey, definition, handler }, true);
  }

  function registerPrompt(name, definition, handler) {
    requireDefinition(name, definition, handler);
    add('prompts', name, { definition, handler });
  }

  function replacePrompt(name, definition, handler) {
    requireDefinition(name, definition, handler);
    add('prompts', name, { definition, handler }, true);
  }

  return {
    registerTool,
    replaceTool,
    removeTool: name => remove('tools', name),
    registerResource,
    replaceResource,
    removeResource: name => remove('resources', name),
    registerPrompt,
    replacePrompt,
    removePrompt: name => remove('prompts', name),
    tools: () => [...entries.tools.values()].map(snapshotEntry).sort((a, b) => a.name.localeCompare(b.name)),
    resources: () => [...entries.resources.values()].map(snapshotEntry).sort((a, b) => a.name.localeCompare(b.name)),
    prompts: () => [...entries.prompts.values()].map(snapshotEntry).sort((a, b) => a.name.localeCompare(b.name)),
    resourcePatterns: () => [...entries.resources.values()].map(entry => entry.policyKey),
    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('MCP catalog listener must be a function');
      listeners.add(listener);
      let active = true;
      return () => {
        if (!active) return;
        active = false;
        listeners.delete(listener);
      };
    },
    revision: () => revision,
    contractFingerprint(features = {}) {
      const contract = { features };
      for (const [kind, singular] of [['tools', 'tool'], ['resources', 'resource'], ['prompts', 'prompt']]) {
        contract[kind] = [...entries[kind].values()]
          .sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)
          .map(entry => {
            const definition = normalizeDefinition(snapshotMetadata(entry.definition), singular);
            if (singular === 'resource') {
              const field = typeof entry.uriOrTemplate === 'string' ? 'uri' : 'uriTemplate';
              return { ...definition, name: entry.name, [field]: entry.policyKey };
            }
            return { ...definition, name: entry.name };
          });
      }
      return createHash('sha256').update(canonicalJSON(contract)).digest('hex');
    },
    fingerprint(kind, canAccess) {
      const catalog = entries[kind];
      if (!catalog) throw new TypeError('unknown MCP catalog kind');
      return JSON.stringify([...catalog.values()]
        .filter(entry => canAccess(kind, snapshotEntry(entry)))
        .map(entry => [entry.name, entry.revision]));
    },
  };
}
