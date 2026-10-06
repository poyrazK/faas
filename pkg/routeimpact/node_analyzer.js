// This script runs only the bundled TypeScript compiler parser. It reads
// source text from stdin and never imports, evaluates, or resolves customer
// modules or dependencies.
const fs = require('node:fs');
const path = require('node:path');
const ts = require(process.argv[1]);

const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const limits = input.limits || {};
const issues = [];
const files = new Map();
const allPaths = new Set((input.sources || []).map((row) => row.file));
const printer = ts.createPrinter({ removeComments: true, newLine: ts.NewLineKind.LineFeed });
let visitedNodes = 0;

function addIssue(code, file, line, message, symbol = '') {
  if (issues.length >= (limits.issues || 500)) throw new Error('issue limit exceeded');
  issues.push({ code, file: file || '', line: line || 0, message, symbol });
}
function lineOf(file, node) {
  return file.source.getLineAndCharacterOfPosition(node.getStart(file.source)).line + 1;
}
function print(file, node) {
  return printer.printNode(ts.EmitHint.Unspecified, node, file.source);
}
function hashText(value) {
  return require('node:crypto').createHash('sha256').update(value).digest('hex');
}
function sourceExtensions() { return ['.ts', '.tsx', '.js', '.jsx', '.mts', '.cts', '.mjs', '.cjs']; }
function resolveLocal(from, specifier) {
  if (!specifier.startsWith('.') && !specifier.startsWith('/')) return '';
  const base = path.posix.normalize(path.posix.join(path.posix.dirname(from), specifier));
  const candidates = [];
  const requestedExt = path.posix.extname(base);
  if (sourceExtensions().includes(requestedExt)) {
    candidates.push(base);
    const stem = base.slice(0, -requestedExt.length);
    const equivalents = requestedExt === '.js' ? ['.ts', '.tsx'] : requestedExt === '.mjs' ? ['.mts'] : requestedExt === '.cjs' ? ['.cts'] : requestedExt === '.jsx' ? ['.tsx'] : [];
    for (const ext of equivalents) candidates.push(stem + ext);
  } else for (const ext of sourceExtensions()) candidates.push(base + ext);
  for (const ext of sourceExtensions()) candidates.push(path.posix.join(base, 'index' + ext));
  return candidates.find((candidate) => allPaths.has(candidate) && files.has(candidate)) || '';
}
function isFunctionLike(node) {
  return ts.isFunctionLike(node) || ts.isMethodDeclaration(node) || ts.isGetAccessor(node) || ts.isSetAccessor(node);
}
function modifiersContain(node, kind) {
  return !!node.modifiers && node.modifiers.some((modifier) => modifier.kind === kind);
}
function expressionName(node) {
  if (ts.isIdentifier(node)) return node.text;
  if (ts.isPropertyAccessExpression(node)) return expressionName(node.expression) + '.' + node.name.text;
  return '';
}
function literalString(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text;
  return null;
}
function callTarget(node) {
  if (!ts.isCallExpression(node)) return null;
  const expr = node.expression;
  return ts.isPropertyAccessExpression(expr) ? { receiver: expr.expression, method: expr.name.text } : null;
}
function symbolID(file, name) { return file.path + '#' + name; }

for (const row of input.sources || []) {
  const body = Buffer.from(row.body || '', 'base64').toString('utf8');
  const ext = path.posix.extname(row.file).toLowerCase();
  const kind = ext === '.tsx' || ext === '.jsx' ? ts.ScriptKind.TSX : ext === '.js' || ext === '.mjs' || ext === '.cjs' ? ts.ScriptKind.JS : ts.ScriptKind.TS;
  const source = ts.createSourceFile(row.file, body, ts.ScriptTarget.Latest, true, kind);
  const file = { path: row.file, source, imports: new Map(), namespaces: new Map(), frameworkBindings: new Map(), objects: new Map(), functions: new Map(), exports: new Map(), moduleDeps: new Set(), routes: [], mounts: [], globals: [], module: null };
  files.set(row.file, file);
  if (source.parseDiagnostics.length) addIssue('javascript_parse_error', row.file, 1, 'A JavaScript or TypeScript source file could not be parsed; route analysis is incomplete.');
}

function moduleKind(specifier) {
  if (specifier === 'express') return 'express';
  if (specifier === 'hono' || specifier.startsWith('hono/') || specifier === '@hono/zod-openapi') return 'hono';
  return '';
}
function localImport(file, specifier) {
  const target = resolveLocal(file.path, specifier);
  if (target) {
    file.moduleDeps.add(target);
    return target;
  }
  return '';
}
function exportNameMap(file, declaration) {
  if (!declaration) return;
  if (ts.isVariableStatement(declaration)) {
    for (const item of declaration.declarationList.declarations) if (ts.isIdentifier(item.name)) file.exports.set(item.name.text, item.name.text);
  } else if (ts.isFunctionDeclaration(declaration) && declaration.name) file.exports.set(declaration.name.text, declaration.name.text);
  else if (ts.isClassDeclaration(declaration) && declaration.name) file.exports.set(declaration.name.text, declaration.name.text);
}

for (const file of files.values()) {
  for (const statement of file.source.statements) {
    if (ts.isImportDeclaration(statement) && ts.isStringLiteral(statement.moduleSpecifier)) {
      const specifier = statement.moduleSpecifier.text;
      const target = localImport(file, specifier);
      const framework = moduleKind(specifier);
      const clause = statement.importClause;
      if (clause) {
        if (clause.name) {
          file.imports.set(clause.name.text, { target, name: 'default' });
          if (framework === 'express') file.frameworkBindings.set(clause.name.text, 'express-factory');
        }
        if (clause.namedBindings && ts.isNamespaceImport(clause.namedBindings)) {
          file.namespaces.set(clause.namedBindings.name.text, target);
          if (framework) file.frameworkBindings.set(clause.namedBindings.name.text, framework + '-namespace');
        } else if (clause.namedBindings && ts.isNamedImports(clause.namedBindings)) {
          for (const item of clause.namedBindings.elements) {
            const imported = (item.propertyName || item.name).text;
            file.imports.set(item.name.text, { target, name: imported });
            if (framework === 'express' && imported === 'Router') file.frameworkBindings.set(item.name.text, 'express-router-factory');
            if (framework === 'hono' && (imported === 'Hono' || imported === 'OpenAPIHono')) file.frameworkBindings.set(item.name.text, 'hono-constructor');
          }
        }
      }
    } else if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) {
        if (!ts.isIdentifier(declaration.name) || !declaration.initializer || !ts.isCallExpression(declaration.initializer)) continue;
        const call = declaration.initializer;
        if (ts.isIdentifier(call.expression) && call.expression.text === 'require' && call.arguments.length && literalString(call.arguments[0])) {
          const specifier = literalString(call.arguments[0]);
          const framework = moduleKind(specifier);
          const target = localImport(file, specifier);
          if (framework === 'express') file.frameworkBindings.set(declaration.name.text, 'express-factory');
          else if (framework === 'hono') file.frameworkBindings.set(declaration.name.text, 'hono-namespace');
          else if (target) file.namespaces.set(declaration.name.text, target);
        }
      }
    } else if (ts.isExportDeclaration(statement)) {
      const target = statement.moduleSpecifier && ts.isStringLiteral(statement.moduleSpecifier) ? localImport(file, statement.moduleSpecifier.text) : '';
      if (statement.exportClause && ts.isNamedExports(statement.exportClause)) {
        for (const item of statement.exportClause.elements) file.exports.set(item.name.text, target ? { target, name: (item.propertyName || item.name).text } : (item.propertyName || item.name).text);
      }
    } else if (ts.isExportAssignment(statement)) {
      file.exports.set('default', expressionName(statement.expression) || '__default_expression__');
    }
  }
}

for (const file of files.values()) {
  for (const statement of file.source.statements) {
    if (ts.isFunctionDeclaration(statement) && statement.name && statement.body) {
      file.functions.set(statement.name.text, { id: symbolID(file, statement.name.text), file: file.path, node: statement, name: statement.name.text });
      if (modifiersContain(statement, ts.SyntaxKind.ExportKeyword)) file.exports.set(statement.name.text, statement.name.text);
      if (modifiersContain(statement, ts.SyntaxKind.DefaultKeyword)) file.exports.set('default', statement.name.text);
    } else if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) {
        if (!ts.isIdentifier(declaration.name) || !declaration.initializer || (!ts.isArrowFunction(declaration.initializer) && !ts.isFunctionExpression(declaration.initializer))) continue;
        const name = declaration.name.text;
        file.functions.set(name, { id: symbolID(file, name), file: file.path, node: declaration.initializer, name });
        if (modifiersContain(statement, ts.SyntaxKind.ExportKeyword)) file.exports.set(name, name);
      }
    }
  }
  for (const statement of file.source.statements) {
    if (!modifiersContain(statement, ts.SyntaxKind.ExportKeyword)) continue;
    exportNameMap(file, statement);
    if (modifiersContain(statement, ts.SyntaxKind.DefaultKeyword) && ts.isFunctionDeclaration(statement) && statement.name) file.exports.set('default', statement.name.text);
  }
}

function exported(filePath, name, seen = new Set()) {
  if (!filePath || !files.has(filePath)) return null;
  const key = filePath + '::' + name;
  if (seen.has(key)) return null;
  seen.add(key);
  const file = files.get(filePath);
  let value = file.exports.get(name);
  if (value && typeof value === 'object') return exported(value.target, value.name, seen);
  if (typeof value === 'string') {
    if (file.functions.has(value)) return file.functions.get(value);
    if (file.objects.has(value)) return file.objects.get(value);
    const localImport = file.imports.get(value);
    if (localImport) return exported(localImport.target, localImport.name, seen);
    return null;
  }
  if (name === 'default' && file.objects.size === 1) return [...file.objects.values()][0];
  return null;
}
function resolveSymbol(file, expr) {
  if (ts.isIdentifier(expr)) {
    if (file.functions.has(expr.text)) return file.functions.get(expr.text);
    const binding = file.imports.get(expr.text);
    return binding ? exported(binding.target, binding.name) : null;
  }
  if (ts.isPropertyAccessExpression(expr) && ts.isIdentifier(expr.expression)) {
    const target = file.namespaces.get(expr.expression.text);
    if (target) return exported(target, expr.name.text);
    const binding = file.imports.get(expr.expression.text);
    if (binding && binding.target) {
      const object = exported(binding.target, binding.name);
      if (object && object.methods && object.methods.has(expr.name.text)) return object.methods.get(expr.name.text);
    }
  }
  return null;
}
function propertyLiteral(object, propertyName) {
  if (!ts.isObjectLiteralExpression(object)) return null;
  for (const member of object.properties) {
    if (!ts.isPropertyAssignment(member)) continue;
    const name = ts.isIdentifier(member.name) || ts.isStringLiteral(member.name) ? member.name.text : '';
    if (name === propertyName) return literalString(member.initializer);
  }
  return null;
}
function honoOpenAPIRouteConfig(file, expression) {
  let config = expression;
  if (ts.isIdentifier(config)) {
    for (const statement of file.source.statements) {
      if (!ts.isVariableStatement(statement)) continue;
      const declaration = statement.declarationList.declarations.find((item) => ts.isIdentifier(item.name) && item.name.text === config.text);
      if (declaration && declaration.initializer) { config = declaration.initializer; break; }
    }
  }
  if (ts.isCallExpression(config)) config = config.arguments[0];
  if (!config || !ts.isObjectLiteralExpression(config)) return null;
  return { method: propertyLiteral(config, 'method'), path: propertyLiteral(config, 'path') };
}

function isFrameworkFactory(file, expr, expected) {
  if (ts.isIdentifier(expr)) return file.frameworkBindings.get(expr.text) === expected;
  if (ts.isPropertyAccessExpression(expr) && ts.isIdentifier(expr.expression)) {
    const binding = file.frameworkBindings.get(expr.expression.text);
    return expected === 'express-router-factory' && (binding === 'express-namespace' || binding === 'express-factory') && expr.name.text === 'Router' ||
      expected === 'express-factory' && binding === 'express-namespace' && expr.name.text === 'default' ||
      expected === 'hono-constructor' && binding === 'hono-namespace' && expr.name.text === 'Hono';
  }
  return false;
}
function newObject(file, name, kind) {
  const object = { id: file.path + '::' + name, file: file.path, name, kind, routes: [], mounts: [], globals: [] };
  file.objects.set(name, object);
  return object;
}
function objectFromExpression(file, expr, name) {
  if (ts.isCallExpression(expr)) {
    if (isFrameworkFactory(file, expr.expression, 'express-factory')) return newObject(file, name, 'express-app');
    if (isFrameworkFactory(file, expr.expression, 'express-router-factory')) return newObject(file, name, 'express-router');
  }
  if (ts.isNewExpression(expr) && isFrameworkFactory(file, expr.expression, 'hono-constructor')) return newObject(file, name, 'hono-app');
  return null;
}
for (const file of files.values()) {
  for (const statement of file.source.statements) {
    if (!ts.isVariableStatement(statement)) continue;
    for (const declaration of statement.declarationList.declarations) {
      if (!ts.isIdentifier(declaration.name) || !declaration.initializer) continue;
      objectFromExpression(file, declaration.initializer, declaration.name.text);
    }
  }
}
const knownNodeFramework = [...files.values()].some((file) => [...file.frameworkBindings.values()].some((name) => name.startsWith('express-') || name.startsWith('hono-')));

function resolveObject(file, expr) {
  if (ts.isIdentifier(expr)) {
    if (file.objects.has(expr.text)) return file.objects.get(expr.text);
    const binding = file.imports.get(expr.text);
    const value = binding ? exported(binding.target, binding.name) : null;
    return value && value.kind ? value : null;
  }
  if (ts.isPropertyAccessExpression(expr) && ts.isIdentifier(expr.expression)) {
    const target = file.namespaces.get(expr.expression.text);
    const value = target ? exported(target, expr.name.text) : null;
    return value && value.kind ? value : null;
  }
  return null;
}
function pathValue(node) { return literalString(node); }
function normalizePath(value, file, line, code = 'dynamic_route_path') {
  if (value === null || !value.startsWith('/')) {
    addIssue(value === null ? code : 'unsupported_route_path', file.path, line, 'A route path must be a literal absolute path to map it safely.');
    return null;
  }
  if (value.includes('*') || value.includes('(') || value.includes(')') || value.includes('?')) {
    addIssue('unsupported_route_path', file.path, line, 'A route path uses wildcard or optional syntax outside the static route model.');
    return null;
  }
  return value.replace(/:([A-Za-z0-9_]+)/g, '{$1}');
}
function joinPath(left, right) {
  const a = left === '/' ? '' : left.replace(/\/$/, '');
  const b = right === '/' ? '' : right.replace(/^\//, '');
  const value = a + '/' + b;
  return value || '/';
}
function staticMethods(node) {
  if (ts.isArrayLiteralExpression(node)) return node.elements.map((entry) => literalString(entry));
  const value = literalString(node);
  return value === null ? null : [value];
}
function isConditionalRegistration(node) {
  for (let parent = node.parent; parent && !ts.isSourceFile(parent); parent = parent.parent) {
    if (ts.isFunctionLike(parent)) return 'function';
    if (ts.isIfStatement(parent) || ts.isSwitchStatement(parent) || ts.isForStatement(parent) || ts.isForInStatement(parent) || ts.isForOfStatement(parent) || ts.isWhileStatement(parent) || ts.isDoStatement(parent) || ts.isTryStatement(parent) || ts.isConditionalExpression(parent)) return 'conditional';
  }
  return '';
}
function resolveInline(file, expr, label) {
  if (!ts.isArrowFunction(expr) && !ts.isFunctionExpression(expr)) return resolveSymbol(file, expr);
  const line = lineOf(file, expr);
  const name = '__inline_' + line + '_' + label;
  const id = symbolID(file, name);
  if (!file.functions.has(name)) file.functions.set(name, { id, file: file.path, node: expr, name });
  return file.functions.get(name);
}
function functionRefs(file, node, omitID = '') {
  const refs = new Set();
  function walk(current) {
    if (current !== node && isFunctionLike(current)) return;
    if (ts.isIdentifier(current)) {
      const value = resolveSymbol(file, current);
      if (value && value.id !== omitID) refs.add(value.id);
    } else if (ts.isPropertyAccessExpression(current)) {
      const value = resolveSymbol(file, current);
      if (value && value.id !== omitID) refs.add(value.id);
    }
    ts.forEachChild(current, walk);
  }
  walk(node);
  return [...refs].sort();
}
const knownGlobalCalls = new Set(['AbortController', 'AbortSignal', 'Array', 'BigInt', 'Blob', 'Boolean', 'Buffer', 'Date', 'Error', 'EvalError', 'File', 'FormData', 'Function', 'Headers', 'Map', 'Number', 'Object', 'Promise', 'RangeError', 'ReadableStream', 'ReferenceError', 'RegExp', 'Request', 'Response', 'Set', 'String', 'Symbol', 'SyntaxError', 'TextDecoder', 'TextEncoder', 'TransformStream', 'TypeError', 'URIError', 'URL', 'URLSearchParams', 'WeakMap', 'WeakSet', 'WebSocket', 'WritableStream', 'atob', 'btoa', 'confirm', 'decodeURI', 'decodeURIComponent', 'encodeURI', 'encodeURIComponent', 'eval', 'fetch', 'isFinite', 'isNaN', 'parseFloat', 'parseInt', 'prompt', 'queueMicrotask', 'requestAnimationFrame', 'setImmediate', 'setInterval', 'setTimeout', 'clearImmediate', 'clearInterval', 'clearTimeout', 'structuredClone', 'next']);
function functionIssues(file, fn) {
  const unresolved = [];
  const executableFunction = (node) => ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node) || ts.isMethodDeclaration(node) || ts.isGetAccessor(node) || ts.isSetAccessor(node);
  function walk(current) {
    if (current !== fn.node && executableFunction(current)) {
      unresolved.push({ code: 'unresolved_node_callback', file: file.path, line: lineOf(file, current), symbol: fn.id, message: 'A nested function or callback is not indexed as a separate route dependency; route impact may miss its behavior.' });
      return;
    }
    if (ts.isCallExpression(current) && ts.isIdentifier(current.expression)) {
      const name = current.expression.text;
      if (!resolveSymbol(file, current.expression) && !file.namespaces.has(name) && !knownGlobalCalls.has(name)) {
        unresolved.push({ code: 'unresolved_node_call', file: file.path, line: lineOf(file, current), symbol: name, message: 'A function call does not resolve to a local function or imported module; route impact may miss its behavior.' });
      }
    } else if (ts.isCallExpression(current) && ts.isPropertyAccessExpression(current.expression) && ts.isIdentifier(current.expression.expression)) {
      const receiver = current.expression.expression.text;
      const localNamespace = file.namespaces.get(receiver);
      const localImport = file.imports.get(receiver);
      const hasLocalTarget = !!localNamespace || !!(localImport && localImport.target);
      if (hasLocalTarget && !resolveSymbol(file, current.expression)) {
        unresolved.push({ code: 'unresolved_node_call', file: file.path, line: lineOf(file, current), symbol: expressionName(current.expression), message: 'A call through a local imported module does not resolve to an indexed function; route impact may miss its behavior.' });
      }
    } else if (ts.isNewExpression(current) && ts.isIdentifier(current.expression) && !knownGlobalCalls.has(current.expression.text) && !resolveSymbol(file, current.expression)) {
      unresolved.push({ code: 'unresolved_node_constructor', file: file.path, line: lineOf(file, current), symbol: current.expression.text, message: 'A constructor does not resolve to an indexed local function or imported module; route impact may miss its behavior.' });
    }
    ts.forEachChild(current, walk);
  }
  walk(fn.node);
  return unresolved;
}
function addRegistration(file, object, method, pathNode, handlers, call, line, routeBuilder = null) {
  const pathText = normalizePath(pathValue(pathNode), file, line);
  if (pathText === null) return;
  if (!handlers.length) {
    addIssue('missing_route_handler', file.path, line, 'A supported route registration has no statically identifiable handler.');
    return;
  }
  const handlerExpr = handlers[handlers.length - 1];
  const handler = resolveInline(file, handlerExpr, method.toLowerCase());
  if (!handler) addIssue('unresolved_route_handler', file.path, line, 'The route handler does not resolve to a local or imported function.', expressionName(handlerExpr));
  const dependencies = new Set();
  const fallback = new Set();
  for (const expr of handlers.slice(0, -1)) {
    const middleware = resolveInline(file, expr, 'middleware');
    if (middleware) dependencies.add(middleware.id);
    else {
      addIssue('unresolved_route_middleware', file.path, line, 'A route middleware expression does not resolve to a local or imported function.');
      fallback.add(file.path);
    }
  }
  const methods = method === 'all' ? ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD'] : [method.toUpperCase()];
  const registration = print(file, routeBuilder || call);
  for (const verb of methods) {
    if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'HEAD', 'TRACE', 'CONNECT'].includes(verb)) {
      addIssue('unsupported_route_method', file.path, line, 'A route method is outside the supported HTTP method set.');
      continue;
    }
    const handlerFile = handler ? files.get(handler.file) : file;
    object.routes.push({ method: verb, path: pathText, handler: handler ? handler.name.replace(/^__inline_\d+_/, 'inline') : 'unresolved', handlerID: handler ? handler.id : '', handlerFile: handler ? handler.file : file.path, sourceLine: handler ? lineOf(handlerFile, handler.node) : line,
      registrationLine: line, registrationHash: hashText(registration), contextFiles: [file.path], dependencyFiles: [], dependencySymbols: [...dependencies], fallbackFiles: [...fallback] });
  }
}

for (const file of files.values()) {
  // Build object exports after object declarations are known.
  for (const [name] of file.objects) if (file.exports.get(name) === undefined) {
    const declaration = file.source.statements.find((statement) => ts.isVariableStatement(statement) && statement.declarationList.declarations.some((decl) => ts.isIdentifier(decl.name) && decl.name.text === name));
    if (declaration && modifiersContain(declaration, ts.SyntaxKind.ExportKeyword)) file.exports.set(name, name);
  }
  for (const statement of file.source.statements) {
    function visit(node) {
      visitedNodes++;
      if (visitedNodes > 2000000) throw new Error('AST node limit exceeded');
      if (isFunctionLike(node) && node !== statement) return;
      if (ts.isCallExpression(node)) {
        const target = callTarget(node);
        if (target) {
          let object = resolveObject(file, target.receiver);
          let routeBuilder = null;
          let routePathNode = null;
          if (!object && ts.isCallExpression(target.receiver)) {
            const builder = callTarget(target.receiver);
            if (builder && builder.method === 'route') {
              object = resolveObject(file, builder.receiver);
              if (object && object.kind.startsWith('express')) { routeBuilder = target.receiver; routePathNode = target.receiver.arguments[0]; }
            }
          }
          if (!object) {
            const method = target.method.toLowerCase();
            const routeMethod = ['get', 'post', 'put', 'patch', 'delete', 'options', 'head', 'trace', 'connect', 'all', 'on', 'route', 'use'].includes(method);
            const pathCandidate = method === 'on' ? node.arguments[1] : node.arguments[0];
            const literalPath = pathCandidate ? literalString(pathCandidate) : null;
            const inFunction = isConditionalRegistration(node) === 'function';
            if (routeMethod && node.arguments.length >= 2 && (literalPath !== null && literalPath.startsWith('/') || knownNodeFramework && inFunction)) {
              addIssue('unresolved_route_receiver', file.path, lineOf(file, node), 'A route registration receiver is not a statically identified Express or Hono application or router.');
            }
          }
          if (object && object.kind) {
            const nested = isConditionalRegistration(node);
            if (nested === 'conditional') {
              addIssue('conditional_route_registration', file.path, lineOf(file, node), 'A route registration is conditional and cannot be assumed active.');
              return;
            }
            if (nested === 'function') {
              addIssue('route_registration_helper', file.path, lineOf(file, node), 'A route registration inside a function is not expanded because the function may run conditionally.');
              return;
            }
            const line = lineOf(file, node);
            const method = target.method.toLowerCase();
            if (method === 'use') {
              const args = [...node.arguments];
              if (object.kind.startsWith('hono')) {
                const scope = args.length ? literalString(args.shift()) : null;
                if (scope === null) addIssue('dynamic_hono_middleware_scope', file.path, line, 'A Hono middleware scope is dynamic or missing and cannot be mapped to a stable route set.');
                else addIssue('hono_middleware_scope_unknown', file.path, line, 'Hono middleware is linked conservatively to all routes because path matching and registration order are not modeled.');
                for (const arg of args) {
                  const middleware = resolveInline(file, arg, 'middleware');
                  if (middleware) object.globals.push(middleware.id);
                  else addIssue('unresolved_global_middleware', file.path, line, 'Mounted Hono middleware does not resolve to a local or imported function.');
                }
                return;
              }
              let mountPrefix = '/';
              if (args.length && literalString(args[0]) !== null) mountPrefix = normalizePath(literalString(args.shift()), file, line, 'dynamic_mount_prefix') || '';
              else if (args.length && !resolveObject(file, args[0]) && !resolveSymbol(file, args[0]) && !ts.isArrowFunction(args[0]) && !ts.isFunctionExpression(args[0])) {
                mountPrefix = normalizePath(null, file, line, 'dynamic_mount_prefix') || '';
              }
              if (!mountPrefix) return;
              for (const arg of args) {
                const child = resolveObject(file, arg);
                if (child) object.mounts.push({ child, prefix: mountPrefix, file: file.path, line, hash: hashText(print(file, node)), middleware: [] });
                else {
                  const symbol = resolveSymbol(file, arg);
                  if (symbol) {
                    object.globals.push(symbol.id);
                    addIssue('express_middleware_scope_unknown', file.path, line, 'Application middleware is linked conservatively to all routes because registration order is not modeled.');
                  }
                  else addIssue('unresolved_global_middleware', file.path, line, 'Mounted middleware does not resolve to a local function or router.');
                }
              }
              return;
            }
            if (method === 'route') return;
            if (method === 'openapi' && object.kind.startsWith('hono')) {
              const config = node.arguments.length ? honoOpenAPIRouteConfig(file, node.arguments[0]) : null;
              const handlers = [...node.arguments].slice(1);
              if (!config || !config.method || !config.path || !handlers.length) {
                addIssue('dynamic_hono_openapi_route', file.path, line, 'A Hono OpenAPI registration needs a statically resolvable literal method, path, and handler.');
                return;
              }
              addRegistration(file, object, config.method.toLowerCase(), ts.factory.createStringLiteral(config.path), handlers, node, line);
              return;
            }
            if (['openapi', 'basepath'].includes(method) && object.kind.startsWith('hono')) {
              addIssue('unsupported_hono_route_registration', file.path, line, 'A Hono route registration or path wrapper is outside the static route model.');
              return;
            }
            let routePath = routePathNode || node.arguments[0];
            let handlerArgs = routeBuilder ? [...node.arguments] : [...node.arguments].slice(1);
            let methods = [method];
            if (method === 'on' && !object.kind.startsWith('hono')) return;
            if (method === 'on' && object.kind.startsWith('hono')) {
              if (node.arguments.length < 3) { addIssue('invalid_route_registration', file.path, line, 'A Hono on() registration is missing its methods, path, or handler.'); return; }
              const values = staticMethods(node.arguments[0]);
              if (!values || values.some((value) => value === null)) { addIssue('dynamic_route_method', file.path, line, 'A dynamic Hono method collection cannot be mapped to a stable route.'); return; }
              methods = values.map((value) => String(value).toLowerCase());
              routePath = node.arguments[1]; handlerArgs = [...node.arguments].slice(2);
            }
            if (!['get', 'post', 'put', 'patch', 'delete', 'options', 'head', 'trace', 'connect', 'all', 'on'].includes(method)) return;
            if (!routePath) return;
            if (method !== 'on' && !routeBuilder && node.arguments.length < 2) return; // Express app.get(settingName).
            if (method === 'all') methods = ['all'];
            for (const verb of methods) addRegistration(file, object, verb, routePath, handlerArgs, node, line, routeBuilder);
          }
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(statement);
  }
}

// Hono route(prefix, child) has the same two-argument shape as a mount.
for (const file of files.values()) {
  for (const statement of file.source.statements) {
    function visit(node) {
      if (isFunctionLike(node) && node !== statement) return;
      if (ts.isCallExpression(node)) {
        const target = callTarget(node);
        const object = target && resolveObject(file, target.receiver);
        if (object && object.kind.startsWith('hono') && target.method === 'route') {
          const line = lineOf(file, node);
          if (node.arguments.length < 2) addIssue('invalid_route_mount', file.path, line, 'A Hono route() mount is missing its prefix or child application.');
          else {
            const prefix = normalizePath(literalString(node.arguments[0]), file, line, 'dynamic_mount_prefix');
            const child = resolveObject(file, node.arguments[1]);
            if (prefix && child) object.mounts.push({ child, prefix, file: file.path, line, hash: hashText(print(file, node)), middleware: [] });
            else if (!child) addIssue('unresolved_route_mount', file.path, line, 'A Hono child application does not resolve to a local route object.');
          }
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(statement);
  }
}

function unique(values) { return [...new Set(values)].sort(); }
function visitObject(object, prefix, context, globals, stack, output, depth = 0) {
  if (depth > (limits.depth || 64)) {
    addIssue('route_mount_depth_exceeded', object.file, 0, 'Nested local router mounts exceed the static graph depth limit.');
    return;
  }
  if (stack.has(object.id)) {
    addIssue('route_mount_cycle', object.file, 0, 'A route object mounts itself through a cycle.');
    return;
  }
  const nextStack = new Set(stack); nextStack.add(object.id);
  const nextContext = unique([...context, object.file]);
  const nextGlobals = unique([...globals, ...object.globals]);
  for (const route of object.routes) {
    if (output.length >= (limits.routes || 1000)) throw new Error('route limit exceeded');
    const effective = joinPath(prefix, route.path);
    const dependencies = unique([...route.dependencySymbols, ...nextGlobals]);
    const dependencyFiles = [];
    for (const id of dependencies) {
      const file = index.symbols[id] ? index.symbols[id].file : '';
      if (file && file !== route.contextFiles[0]) dependencyFiles.push(file);
    }
    const routePrefix = route.registrationHash;
    output.push({ method: route.method, path: effective, handler: route.handler, source: { file: route.handlerFile, line: route.sourceLine }, registration: { file: object.file, line: route.registrationLine },
      registration_hash: hashText(routePrefix + '\0' + context.map((value) => value).join('\0')), context_files: nextContext, dependency_files: unique([...route.dependencyFiles, ...dependencyFiles]),
      handler_symbol: route.handlerID, dependency_symbols: dependencies, fallback_files: unique(route.fallbackFiles) });
  }
  for (const mount of object.mounts) {
    const mountPrefix = joinPath(prefix, mount.prefix);
    const prior = output.length;
    visitObject(mount.child, mountPrefix, unique([...nextContext, mount.file]), nextGlobals, nextStack, output, depth + 1);
    for (let i = prior; i < output.length; i++) output[i].registration_hash = hashText(output[i].registration_hash + '\0' + mount.hash);
  }
}

const index = { routes: [], dependencies: {}, issues, modules: {}, symbols: {} };
for (const file of files.values()) {
  index.dependencies[file.path] = [...file.moduleDeps].sort();
  const whole = print(file, file.source);
  const initialization = file.source.statements.filter((statement) => !ts.isFunctionDeclaration(statement) && !ts.isClassDeclaration(statement)).map((statement) => print(file, statement)).join('\n');
  index.modules[file.path] = { hash: hashText(whole), initialization_hash: hashText(initialization), initialization_symbols: [], issues: [] };
  for (const fn of file.functions.values()) {
    const key = fn.id;
    index.symbols[key] = { name: key, file: file.path, line: lineOf(file, fn.node), hash: hashText(print(file, fn.node)), references: functionRefs(file, fn.node, key), issues: functionIssues(file, fn) };
  }
}
let importEdgeCount = 0;
let symbolEdgeCount = 0;
let symbolIssueCount = 0;
for (const value of Object.values(index.dependencies)) importEdgeCount += value.length;
for (const value of Object.values(index.symbols)) {
  symbolEdgeCount += value.references.length;
  symbolIssueCount += value.issues.length;
}
if (importEdgeCount > (limits.edges || 10000) || symbolEdgeCount > (limits.symbol_edges || 20000) || symbolIssueCount > (limits.symbol_issues || 2000) || Object.keys(index.symbols).length > (limits.symbols || 10000)) throw new Error('static graph limit exceeded');

const roots = [];
const applicationObjects = [];
const mountedObjects = new Set();
for (const file of files.values()) for (const object of file.objects.values()) for (const mount of object.mounts) mountedObjects.add(mount.child.id);
for (const file of files.values()) for (const object of file.objects.values()) if (object.kind.endsWith('-app')) {
  applicationObjects.push(object);
  if (!mountedObjects.has(object.id)) roots.push(object);
}
if (roots.length === 0 && applicationObjects.length > 0) roots.push(...applicationObjects);
if (roots.length > 1) addIssue('multiple_node_applications', '', 0, 'Multiple Express or Hono application objects were found; route ownership may be ambiguous.');
const seenRoutes = new Set();
for (const root of roots) visitObject(root, '/', [], [], new Set(), index.routes);
for (const route of index.routes) {
  const key = route.method + ' ' + route.path;
  if (seenRoutes.has(key)) addIssue('duplicate_route', route.registration.file, route.registration.line, 'Multiple route registrations have the same effective method and path.');
  else seenRoutes.add(key);
}
if (!roots.length) addIssue('node_application_unavailable', '', 0, 'No module-level Express or Hono application instance was found.');
process.stdout.write(JSON.stringify(index));
