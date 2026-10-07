import { UriTemplate } from '@modelcontextprotocol/server';

// Scope policies belong to this application. Headers, arguments and MCP
// annotations never grant permissions; authInfo must come from verified JWT
// middleware. Each configured map is an allowlist: omitted entries are denied.
export function createScopePolicy(auth, field, { resource = false } = {}) {
  const configured = Object.hasOwn(auth, field);
  const input = auth[field];
  const rules = new Map();
  if (configured) {
    if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error(`${field} must be an object`);
    for (const [name, scopes] of Object.entries(input)) {
      if (resource) validateResourceKey(name);
      else if (!name || /[\x00-\x20\x7f]/.test(name)) throw new Error(`Use nonempty ${field} keys without ASCII whitespace or controls`);
      validateScopes(scopes);
      if (auth.mode === 'open' && scopes.length) throw new Error(`Scoped ${field} entries require external-oauth`);
      rules.set(name, [...scopes]);
    }
  }

  const templates = resource
    ? [...rules.keys()].filter(pattern => UriTemplate.isTemplate(pattern)).map(pattern => [pattern, new UriTemplate(pattern)])
    : [];

  function matchingRule(identifier) {
    if (typeof identifier !== 'string') return undefined;
    if (rules.has(identifier)) return rules.get(identifier);
    if (!resource) return undefined;
    let found = false;
    const required = new Set();
    for (const [pattern, template] of templates) {
      if (template.match(identifier) === null) continue;
      found = true;
      for (const scope of rules.get(pattern)) required.add(scope);
    }
    return found ? [...required] : undefined;
  }

  function requiredScopes(name) {
    return configured ? matchingRule(name) : [];
  }
  function canAccess(name, authInfo) {
    const required = requiredScopes(name);
    if (!required) return false; // Unlisted entries are denied, including on public servers.
    if (auth.mode === 'open') return true;
    if (!authInfo || !Array.isArray(authInfo.scopes)) return false;
    return [...auth.scopes, ...required].every(scope => authInfo.scopes.includes(scope));
  }
  const label = field.slice(0, -7);
  return {
    requiredScopes,
    canAccess,
    scopes: () => [...new Set([...rules.values()].flat())],
    guard(name, callback, denied = `${label[0].toUpperCase()}${label.slice(1)} access denied`) {
      return (...args) => {
        const ctx = args.at(-1);
        if (!canAccess(name, ctx?.http?.authInfo)) throw new Error(denied);
        return callback(...args);
      };
    },
  };
}

export function createToolPolicy(auth) {
  return createScopePolicy(auth, 'tool_scopes');
}

export function createResourcePolicy(auth) {
  return createScopePolicy(auth, 'resource_scopes', { resource: true });
}

export function createPromptPolicy(auth) {
  return createScopePolicy(auth, 'prompt_scopes');
}

function validateResourceKey(value) {
  if (!value || /[\x00-\x20\x7f]/.test(value)) throw new Error('Resource policy keys must be absolute URIs or URI templates without whitespace or controls');
  try {
    const isTemplate = UriTemplate.isTemplate(value);
    if (!isTemplate && /[{}]/.test(value)) throw new Error();
    const expanded = isTemplate ? new UriTemplate(value).toString() : value;
    const uri = new URL(expanded.replace(/\{[^{}]+\}/g, 'placeholder'));
    if (!uri.protocol || uri.username || uri.password || uri.hash) throw new Error();
  } catch {
    throw new Error('Resource policy keys must be absolute URIs or valid URI templates');
  }
}

export function validateScopes(scopes) {
  if (!Array.isArray(scopes) || scopes.some(s => typeof s !== 'string' || !/^[\x21\x23-\x5b\x5d-\x7e]+$/.test(s))) throw new Error('Scopes must be an explicit array of nonempty ASCII tokens');
}
