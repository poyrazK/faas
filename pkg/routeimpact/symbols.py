"""Static module-level Python function references; concatenated before fastapi.py."""
import ast
import copy
import hashlib


class SymbolGraph:
    SAFE_BUILTINS = {
        "abs", "all", "any", "ascii", "bin", "bool", "bytearray", "bytes", "callable",
        "chr", "complex", "dict", "divmod", "enumerate", "filter", "float", "format",
        "frozenset", "hash", "hex", "int", "isinstance", "issubclass", "iter", "len",
        "list", "map", "max", "min", "next", "oct", "ord", "pow", "print", "range",
        "repr", "reversed", "round", "set", "slice", "sorted", "str", "sum", "tuple",
        "type", "zip", "super", "Exception", "ValueError", "TypeError", "RuntimeError",
        "KeyError", "IndexError", "NotImplementedError", "StopIteration", "AssertionError",
    }
    DYNAMIC_BUILTINS = {"getattr", "setattr", "delattr", "globals", "locals", "vars", "eval", "exec", "__import__"}

    def __init__(self, index):
        self.index = index
        self.symbols = {}
        self.modules = {}
        self.bindings = {}
        self.issue_count = 0
        self.edge_count = 0
        self.initializers = {}
        for path, (module, tree) in index.files.items():
            counts = {}
            initializers = []
            for node in tree.body:
                for name in self.bound_names(node):
                    counts[name] = counts.get(name, 0) + (1 if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Import, ast.ImportFrom, ast.Assign, ast.AnnAssign, ast.AugAssign)) else 2)
                if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    # The body does not run at import time, but decorators,
                    # defaults, annotations, and the binding still matter.
                    stub = copy.copy(node)
                    stub.body = [ast.Pass()]
                    initializers.append(stub)
                    name = (module + "." if module else "") + node.name
                    self.symbols[name] = {
                        "name": name, "file": path, "line": node.lineno,
                        "hash": self.fingerprint(node), "references": [], "fallback_files": [], "issues": [],
                    }
                    if len(self.symbols) > index.limits["symbols"]:
                        raise ValueError("route impact symbol limit exceeded")
                else:
                    initializers.append(node)
            self.initializers[path] = initializers
            self.bindings[module] = counts
            self.modules[path] = {
                "hash": self.fingerprint(tree),
                "initialization_hash": self.fingerprint(ast.Module(body=initializers, type_ignores=[])),
            }
        # A module can rebind an imported function through attribute writes.
        # Reject those targets as stable functions even if the import alias is
        # unchanged. Do not enter function bodies while checking initializers.
        for path, (module, tree) in index.files.items():
            def mutation(node):
                if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                    return
                if isinstance(node, (ast.Assign, ast.AnnAssign, ast.AugAssign, ast.Delete)):
                    targets = node.targets if isinstance(node, (ast.Assign, ast.Delete)) else [node.target]
                    for target in targets:
                        if isinstance(target, ast.Attribute):
                            name = index.expression(module, target)
                            if name:
                                owner, _, variable = name.rpartition(".")
                                if owner in self.bindings:
                                    self.bindings[owner][variable] = self.bindings[owner].get(variable, 0) + 2
                for child in ast.iter_child_nodes(node):
                    mutation(child)
            for node in tree.body:
                mutation(node)
        for path, (module, tree) in index.files.items():
            for node in tree.body:
                if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    name = (module + "." if module else "") + node.name
                    self.analyze(module, node, self.symbols[name])

    @staticmethod
    def fingerprint(node):
        # Locations, comments, and formatting never enter this fingerprint.
        return hashlib.sha256(ast.dump(node, include_attributes=False).encode("utf-8")).hexdigest()

    @staticmethod
    def bound_names(node):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            return [node.name]
        if isinstance(node, ast.Import):
            return [alias.asname or alias.name.split(".")[0] for alias in node.names]
        if isinstance(node, ast.ImportFrom):
            return [alias.asname or alias.name for alias in node.names]
        if isinstance(node, (ast.Assign, ast.AnnAssign, ast.AugAssign)):
            targets = node.targets if isinstance(node, ast.Assign) else [node.target]
            return [value.id for target in targets for value in ast.walk(target) if isinstance(value, ast.Name) and isinstance(value.ctx, ast.Store)]
        if isinstance(node, ast.Name) and isinstance(node.ctx, (ast.Store, ast.Del)):
            return [node.id]
        names = []
        # Includes loop targets, exception names, pattern captures, and walrus
        # bindings; nested function/class bodies were excluded above.
        if isinstance(node, ast.ExceptHandler) and node.name:
            names.append(node.name)
        for kind, attribute in [("MatchAs", "name"), ("MatchStar", "name"), ("MatchMapping", "rest")]:
            if type(node).__name__ == kind and getattr(node, attribute, None):
                names.append(getattr(node, attribute))
        for value in ast.iter_child_nodes(node):
            names.extend(SymbolGraph.bound_names(value))
        return names

    def issue(self, symbol, node, code, message):
        issue = {"code": code, "file": symbol["file"], "line": node.lineno,
                 "symbol": symbol["name"], "message": message}
        if issue not in symbol["issues"]:
            self.issue_count += 1
            if self.issue_count > self.index.limits["symbol_issues"]:
                raise ValueError("route impact symbol issue limit exceeded")
            symbol["issues"].append(issue)

    def canonical(self, name, seen=None):
        if not name:
            return None
        seen = set() if seen is None else seen
        if name in seen:
            return None
        if len(seen) >= self.index.limits["depth"]:
            raise ValueError("route impact alias depth limit exceeded")
        seen.add(name)
        module, _, variable = name.rpartition(".")
        if self.bindings.get(module, {}).get(variable, 0) > 1:
            return None
        alias = self.index.aliases.get((module, variable))
        if isinstance(alias, str):
            return self.canonical(alias, seen)
        if alias is not None:
            return self.resolve(module, alias, set(), {}, seen)
        return name

    def resolve(self, module, node, local_names, local_imports, seen=None):
        if isinstance(node, ast.Attribute):
            owner = self.resolve(module, node.value, local_names, local_imports, seen)
            return self.canonical(owner + "." + node.attr, seen) if owner else None
        if not isinstance(node, ast.Name):
            return None
        if node.id in local_imports:
            return self.canonical(local_imports[node.id], seen)
        if node.id in local_names:
            return None
        return self.canonical((module + "." if module else "") + node.id, seen)

    def external_import(self, module, node, local_names, local_imports):
        root = node
        while isinstance(root, ast.Attribute):
            root = root.value
        if not isinstance(root, ast.Name):
            return False
        if root.id in local_imports:
            imported = local_imports[root.id]
        elif root.id not in local_names and self.bindings.get(module, {}).get(root.id, 0) <= 1 and isinstance(self.index.aliases.get((module, root.id)), str):
            imported = self.index.aliases[(module, root.id)]
        else:
            return False
        return not self.index.module_paths(imported)

    def analyze(self, module, function, symbol):
        local_names = {arg.arg for arg in function.args.posonlyargs + function.args.args + function.args.kwonlyargs}
        for arg in [function.args.vararg, function.args.kwarg]:
            if arg:
                local_names.add(arg.arg)
        writes = {}
        local_imports = {}
        nodes = []

        def walk(node, outer=False):
            nodes.append((node, outer))
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Lambda, ast.ListComp, ast.SetComp, ast.DictComp, ast.GeneratorExp)):
                self.issue(symbol, node, "nested_scope", "Nested functions, classes, and factories require module fallback.")
                if hasattr(node, "name"):
                    local_names.add(node.name)
                return
            for child in ast.iter_child_nodes(node):
                walk(child, outer)

        # Route decorators establish registration; they are not calls from the
        # handler. Other decorators and defaults can reference local helpers.
        expressions = list(function.args.defaults)
        expressions += [value for value in function.args.kw_defaults if value is not None]
        expressions += [arg.annotation for arg in function.args.posonlyargs + function.args.args + function.args.kwonlyargs + [arg for arg in [function.args.vararg, function.args.kwarg] if arg] if arg.annotation]
        if function.returns:
            expressions.append(function.returns)
        annotations = [arg.annotation for arg in function.args.posonlyargs + function.args.args + function.args.kwonlyargs + [arg for arg in [function.args.vararg, function.args.kwarg] if arg] if arg.annotation]
        if function.returns:
            annotations.append(function.returns)
        for annotation in annotations:
            if isinstance(annotation, ast.Constant) and isinstance(annotation.value, str) and (annotation.value not in self.SAFE_BUILTINS or self.bindings.get(module, {}).get(annotation.value)):
                self.issue(symbol, annotation, "quoted_annotation", "Quoted type or dependency annotations require module fallback.")
        for decorator in function.decorator_list:
            if isinstance(decorator, ast.Call) and isinstance(decorator.func, ast.Attribute) and self.index.object_key(module, decorator.func.value):
                continue
            self.issue(symbol, decorator, "decorated_symbol", "Custom function decorators can replace bindings; using module fallback.")
            expressions.append(decorator)
        for expression in expressions:
            walk(expression, True)
        for statement in function.body:
            walk(statement)
        for node, outer in nodes:
            if outer:
                continue
            if isinstance(node, ast.Name) and isinstance(node.ctx, (ast.Store, ast.Del)):
                local_names.add(node.id)
                writes[node.id] = writes.get(node.id, 0) + 1
            elif isinstance(node, (ast.Assign, ast.AnnAssign, ast.AugAssign, ast.Delete)):
                targets = node.targets if isinstance(node, (ast.Assign, ast.Delete)) else [node.target]
                if any(isinstance(target, (ast.Attribute, ast.Subscript)) for target in targets):
                    self.issue(symbol, node, "mutable_binding", "Attribute or collection writes may change callable bindings; using module fallback.")
            elif isinstance(node, ast.ExceptHandler) and node.name:
                local_names.add(node.name)
                writes[node.name] = writes.get(node.name, 0) + 1
            elif type(node).__name__ in {"MatchAs", "MatchStar", "MatchMapping"}:
                name = getattr(node, "name", None) or getattr(node, "rest", None)
                if name:
                    local_names.add(name)
                    writes[name] = writes.get(name, 0) + 1
            elif isinstance(node, (ast.Global, ast.Nonlocal)):
                local_names.update(node.names)
                self.issue(symbol, node, "mutable_binding", "Global or nonlocal bindings require module fallback.")
            elif isinstance(node, (ast.Import, ast.ImportFrom)):
                for name in self.bound_names(node):
                    local_names.add(name)
                    writes[name] = writes.get(name, 0) + 1
                if node not in function.body:
                    self.issue(symbol, node, "conditional_symbol_import", "Conditional local imports cannot establish a stable function binding.")
                    continue
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        local_imports[alias.asname or alias.name.split(".")[0]] = alias.name if alias.asname else alias.name.split(".")[0]
                else:
                    name = self.index.imported(module, node)
                    if name is not None:
                        for alias in node.names:
                            local_imports[alias.asname or alias.name] = (name + "." if name else "") + alias.name
        for name in list(local_imports):
            if writes.get(name, 0) > 1:
                del local_imports[name]
        if self.bindings.get(module, {}).get(function.name, 0) > 1:
            self.issue(symbol, function, "ambiguous_symbol_binding", "Repeated function bindings cannot establish the registered handler's identity.")
        references = set()
        fallback_files = set()
        for node, outer in nodes:
            names, imports = (set(), {}) if outer else (local_names, local_imports)
            if isinstance(node, (ast.Name, ast.Attribute)) and not isinstance(getattr(node, "ctx", None), (ast.Store, ast.Del)):
                name = self.resolve(module, node, names, imports)
                if name in self.symbols:
                    references.add(name)
                elif outer and name:
                    root = node
                    while isinstance(root, ast.Attribute):
                        root = root.value
                    if isinstance(root, ast.Name) and root.id in self.bindings.get(module, {}):
                        fallback_files.update(self.index.module_paths(name))
                elif name is None and self.index.expression(module, node) in self.symbols and (outer or not isinstance(node, ast.Name) or node.id not in names):
                    self.issue(symbol, node, "unresolved_symbol_reference", "A function reference has a shadowed, conditional, or repeated binding; using module fallback.")
            if not isinstance(node, ast.Call):
                continue
            name = self.resolve(module, node.func, names, imports)
            if name in self.symbols:
                continue
            bare_builtin = isinstance(node.func, ast.Name) and node.func.id not in names and not self.bindings.get(module, {}).get(node.func.id)
            if (bare_builtin and node.func.id in self.DYNAMIC_BUILTINS) or name in {"builtins." + value for value in self.DYNAMIC_BUILTINS}:
                self.issue(symbol, node, "dynamic_symbol_lookup", "Dynamic symbol lookup or execution requires module fallback.")
            elif bare_builtin and node.func.id in self.SAFE_BUILTINS:
                continue
            elif self.external_import(module, node.func, names, imports):
                # Installed libraries are outside the local source graph. A
                # local callback passed to them still contributes a reference.
                continue
            else:
                self.issue(symbol, node, "unresolved_symbol_call", "This call does not resolve to a stable module-level function; using module fallback.")
        self.edge_count += len(references)
        if self.edge_count > self.index.limits["symbol_edges"]:
            raise ValueError("route impact symbol edge limit exceeded")
        symbol["references"] = sorted(references)
        symbol["fallback_files"] = sorted(fallback_files)
        symbol["issues"].sort(key=lambda row: (row["line"], row["code"]))

    def initializer(self, module, path, nodes):
        roots = set()
        issues = []
        def walk(node):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                for child in node.decorator_list + node.args.defaults + [value for value in node.args.kw_defaults if value is not None]:
                    walk(child)
                for arg in node.args.posonlyargs + node.args.args + node.args.kwonlyargs + [arg for arg in [node.args.vararg, node.args.kwarg] if arg]:
                    if arg.annotation:
                        walk(arg.annotation)
                if node.returns:
                    walk(node.returns)
                return
            if isinstance(node, ast.Attribute):
                owner = self.resolve(module, node.value, set(), {})
                if owner in self.symbols:
                    roots.add(owner)
            if isinstance(node, ast.Call):
                name = self.resolve(module, node.func, set(), {})
                framework = name and (name.startswith("fastapi.") or any(name == key or name.startswith(key + ".") for key in self.index.objects))
                builtin = isinstance(node.func, ast.Name) and not self.bindings.get(module, {}).get(node.func.id) and node.func.id in self.SAFE_BUILTINS
                dynamic = (isinstance(node.func, ast.Name) and not self.bindings.get(module, {}).get(node.func.id) and node.func.id in self.DYNAMIC_BUILTINS) or name in {"builtins." + value for value in self.DYNAMIC_BUILTINS}
                if name in self.symbols:
                    roots.add(name)
                elif dynamic or (not framework and not builtin and not self.external_import(module, node.func, set(), {})):
                    issue = {"code": "unresolved_initializer_call", "file": path, "line": node.lineno,
                             "message": "A module initialization call cannot be resolved; using module fallback."}
                    if issue not in issues:
                        self.issue_count += 1
                        if self.issue_count > self.index.limits["symbol_issues"]:
                            raise ValueError("route impact symbol issue limit exceeded")
                        issues.append(issue)
                # Non-framework calls may invoke callbacks passed as arguments.
                if not framework:
                    for argument in node.args + [keyword.value for keyword in node.keywords]:
                        for value in ast.walk(argument):
                            if isinstance(value, (ast.Name, ast.Attribute)):
                                reference = self.resolve(module, value, set(), {})
                                if reference in self.symbols:
                                    roots.add(reference)
            for child in ast.iter_child_nodes(node):
                walk(child)
        for node in nodes:
            walk(node)
        self.edge_count += len(roots)
        if self.edge_count > self.index.limits["symbol_edges"]:
            raise ValueError("route impact symbol edge limit exceeded")
        return sorted(roots), sorted(issues, key=lambda row: (row["line"], row["code"]))

    def result(self):
        for path, (module, tree) in self.index.files.items():
            roots, issues = self.initializer(module, path, self.initializers[path])
            self.modules[path]["initialization_symbols"] = roots
            self.modules[path]["issues"] = issues
        return {"modules": self.modules, "symbols": self.symbols}
