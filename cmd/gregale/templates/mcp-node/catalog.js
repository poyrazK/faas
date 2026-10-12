import { createHash } from 'node:crypto';

const NAME_PATTERN = /^[\x21-\x7e]{1,128}$/;
const RESOURCE_VARIABLE_PATTERN = /\{([A-Za-z][A-Za-z0-9_.-]*)\}/g;

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
    revision += 1;
    catalog.set(name, { ...entry, name, revision,
      ...(entry.definition ? { definition: { ...entry.definition } } : {}) });
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
    tools: () => [...entries.tools.values()].sort((a, b) => a.name.localeCompare(b.name)),
    resources: () => [...entries.resources.values()].sort((a, b) => a.name.localeCompare(b.name)),
    prompts: () => [...entries.prompts.values()].sort((a, b) => a.name.localeCompare(b.name)),
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
            const definition = normalizeDefinition(entry.definition, singular);
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
        .filter(entry => canAccess(kind, entry))
        .map(entry => [entry.name, entry.revision]));
    },
  };
}
