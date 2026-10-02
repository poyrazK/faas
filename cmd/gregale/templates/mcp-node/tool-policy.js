// Tool policy belongs to this application. Headers, arguments and annotations
// never grant permissions; authInfo must come from the verified JWT middleware.
export function createToolPolicy(auth) {
  const configured = Object.hasOwn(auth, 'tool_scopes');
  const rules = new Map();
  if (configured) {
    if (!auth.tool_scopes || typeof auth.tool_scopes !== 'object' || Array.isArray(auth.tool_scopes)) throw new Error('tool_scopes must be an object');
    for (const [name, scopes] of Object.entries(auth.tool_scopes)) {
      if (!name || /[\x00-\x20\x7f]/.test(name)) throw new Error('Use nonempty tool names without ASCII whitespace or controls');
      validateScopes(scopes);
      if (auth.mode === 'open' && scopes.length) throw new Error('Scoped tools require external-oauth');
      rules.set(name, [...scopes]);
    }
  }
  function requiredScopes(name) {
    return configured ? rules.get(name) : [];
  }
  function canAccess(name, authInfo) {
    const required = requiredScopes(name);
    if (!required) return false; // Unlisted tools are denied, including on public servers.
    if (auth.mode === 'open') return true;
    if (!authInfo || !Array.isArray(authInfo.scopes)) return false;
    return [...auth.scopes, ...required].every(scope => authInfo.scopes.includes(scope));
  }
  return {
    requiredScopes,
    canAccess,
    guard(name, callback, onDenied = () => {}) {
      return (args, ctx) => {
        if (!canAccess(name, ctx.http?.authInfo)) { onDenied(); throw new Error('Tool access denied'); }
        return callback(args, ctx);
      };
    },
  };
}

export function validateScopes(scopes) {
  if (!Array.isArray(scopes) || scopes.some(s => typeof s !== 'string' || !/^[\x21\x23-\x5b\x5d-\x7e]+$/.test(s))) throw new Error('Scopes must be an explicit array of nonempty ASCII tokens');
}
