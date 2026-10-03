"""Static, bounded FastAPI indexer. Run with python3 -I -S; never import sources."""
import ast
import base64
import json
import sys


class Indexer:
    METHODS = {"get", "post", "put", "patch", "delete", "options", "head", "trace"}

    def __init__(self, request):
        self.request = request
        self.limits = request["limits"]
        self.files = {}
        self.modules = {}
        self.aliases = {}
        self.objects = {}
        self.edges = {}
        self.declarations = {}
        self.dependencies = {}
        self.load_dependencies = {}
        self.loaded = set()
        self.functions = {}
        self.issues = []
        self.routes = []
        for source in request["sources"]:
            path = source["file"]
            name = path[:-3].replace("/", ".")
            if name.endswith(".__init__"):
                name = name[:-9]
            elif name == "__init__":
                name = ""
            if name in self.modules:
                self.issue("ambiguous_module", path, 1, "A module and package have the same import name.")
            self.modules[name] = path
            try:
                # Parsing bytes respects Python source encoding declarations.
                tree = ast.parse(base64.b64decode(source["body"]), filename=path)
                self.files[path] = (name, tree)
            except (SyntaxError, ValueError, RecursionError):
                self.issue("parse_failed", path, 1, "Python source could not be parsed by the installed interpreter.")
        for path, (module, tree) in self.files.items():
            for node in tree.body:
                if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    name = (module + "." if module else "") + node.name
                    self.functions[name] = {"name": node.name, "file": path, "line": node.lineno, "symbol": name}
        if "fastapi" in self.modules:
            self.issue("shadowed_framework", self.modules["fastapi"], 1,
                       "A local fastapi module can shadow the framework; its identity cannot be verified.")

    def issue(self, code, path, line, message):
        if len(self.issues) >= self.limits["issues"]:
            raise ValueError("route impact issue limit exceeded")
        issue = {"code": code, "file": path, "line": line, "message": message}
        if issue not in self.issues:
            self.issues.append(issue)

    def imported(self, module, node):
        if not node.level:
            return node.module or ""
        path = self.modules.get(module, "")
        package = module.split(".") if path.endswith("__init__.py") else module.split(".")[:-1]
        if node.level > len(package):
            return None
        package = package[:len(package) - node.level + 1]
        return ".".join(package + ([node.module] if node.module else []))

    def expression(self, module, node, seen=None):
        if isinstance(node, ast.Attribute):
            owner = self.expression(module, node.value, seen)
            return owner + "." + node.attr if owner else None
        if not isinstance(node, ast.Name):
            return None
        key = (module, node.id)
        seen = set() if seen is None else seen
        if key in seen:
            return None
        seen.add(key)
        alias = self.aliases.get(key)
        if isinstance(alias, str):
            target_module, _, target_name = alias.rpartition(".")
            if (target_module, target_name) in self.aliases:
                return self.expression(target_module, ast.Name(id=target_name), seen)
            return alias
        if alias is not None:
            return self.expression(module, alias, seen)
        return module + "." + node.id if module else node.id

    def object_key(self, module, node):
        name = self.expression(module, node)
        return name if name in self.objects else None

    def owned_expression(self, module, node):
        name = self.expression(module, node)
        return name and any(name == key or name.startswith(key + ".") for key in self.objects)

    @staticmethod
    def argument(call, name, position=None):
        for keyword in call.keywords:
            if keyword.arg == name:
                return keyword.value
        if position is not None and len(call.args) > position:
            return call.args[position]
        return None

    @staticmethod
    def literal(node, default=None):
        if node is None:
            return default
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            return node.value
        return None

    def module_paths(self, name):
        parts = name.split(".")
        paths = set()
        for length in range(1, len(parts) + 1):
            prefix = ".".join(parts[:length])
            if prefix in self.modules:
                paths.add(self.modules[prefix])
        return paths

    def imports(self):
        for path, (module, tree) in self.files.items():
            linked = set()
            loaded = set()
            for node in tree.body:
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        loaded.update(self.module_paths(alias.name))
                elif isinstance(node, ast.ImportFrom):
                    name = self.imported(module, node)
                    if name is not None:
                        loaded.update(self.module_paths(name))
                        for alias in node.names:
                            loaded.update(self.module_paths((name + "." if name else "") + alias.name))
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        linked.update(self.module_paths(alias.name))
                elif isinstance(node, ast.ImportFrom):
                    name = self.imported(module, node)
                    if name is None:
                        self.issue("unresolved_relative_import", path, node.lineno, "Relative import escapes the selected source root.")
                        continue
                    linked.update(self.module_paths(name))
                    for alias in node.names:
                        if alias.name == "*":
                            self.issue("star_import", path, node.lineno, "Wildcard imports cannot establish route object bindings.")
                        else:
                            linked.update(self.module_paths((name + "." if name else "") + alias.name))
                elif isinstance(node, ast.Call):
                    name = self.expression(module, node.func)
                    if name in {"__import__", "importlib.import_module", "importlib.util.spec_from_file_location"} or (name and name.endswith(".__import__")):
                        self.issue("dynamic_import", path, node.lineno, "Dynamic imports are outside the local import graph.")
            # Importing a package module also initializes its parent packages.
            for length in range(1, len(module.split("."))):
                parents = self.module_paths(".".join(module.split(".")[:length]))
                linked.update(parents)
                loaded.update(parents)
            linked.discard(path)
            loaded.discard(path)
            self.dependencies[path] = sorted(linked)
            self.load_dependencies[path] = sorted(loaded)
            if sum(len(values) for values in self.dependencies.values()) > self.limits["edges"]:
                raise ValueError("route impact import edge limit exceeded")

    def bindings(self):
        for path, (module, tree) in self.files.items():
            for node in tree.body:
                if isinstance(node, ast.Import):
                    for alias in node.names:
                        self.aliases[(module, alias.asname or alias.name.split(".")[0])] = alias.name if alias.asname else alias.name.split(".")[0]
                elif isinstance(node, ast.ImportFrom):
                    name = self.imported(module, node)
                    if name is not None:
                        for alias in node.names:
                            self.aliases[(module, alias.asname or alias.name)] = (name + "." if name else "") + alias.name
                elif isinstance(node, (ast.Assign, ast.AnnAssign)):
                    targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                    for target in targets:
                        if isinstance(target, ast.Name) and isinstance(node.value, (ast.Name, ast.Attribute)):
                            self.aliases[(module, target.id)] = node.value
        # Class bindings precede object creation, including objects in another
        # module imported under an alias.
        for path, (module, tree) in self.files.items():
            for node in tree.body:
                if not isinstance(node, (ast.Assign, ast.AnnAssign)) or not isinstance(node.value, ast.Call):
                    continue
                kind = self.expression(module, node.value.func)
                if kind not in {"fastapi.FastAPI", "fastapi.applications.FastAPI", "fastapi.APIRouter", "fastapi.routing.APIRouter"}:
                    continue
                targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                if len(targets) != 1 or not isinstance(targets[0], ast.Name):
                    self.issue("dynamic_binding", path, node.lineno, "Application and router bindings must be simple module-level names.")
                    continue
                key = (module + "." if module else "") + targets[0].id
                prefix = self.literal(self.argument(node.value, "prefix"), "")
                if prefix is None or (prefix and (not prefix.startswith("/") or prefix.endswith("/"))):
                    self.issue("dynamic_prefix", path, node.lineno, "Router prefixes must be literal valid path strings.")
                    prefix = None
                self.objects[key] = {"kind": "app" if kind.endswith(".FastAPI") else "router", "prefix": prefix,
                                     "file": path, "line": node.lineno, "dependencies": [], "symbols": [], "fallback": [], "shared": []}
                self.edges[key] = []
                self.declarations[key] = []
                if any(keyword.arg is None for keyword in node.value.keywords) or any(isinstance(arg, ast.Starred) for arg in node.value.args):
                    self.issue("dynamic_arguments", path, node.lineno, "Expanded application or router constructor arguments cannot be resolved statically.")
                if self.argument(node.value, "routes") is not None:
                    self.issue("unsupported_registration", path, node.lineno, "Constructor-supplied route lists are outside this HTTP index.")
        self.symbol_graph = SymbolGraph(self)
        for path, (module, tree) in self.files.items():
            for node in tree.body:
                if isinstance(node, (ast.Assign, ast.AnnAssign)) and isinstance(node.value, ast.Call):
                    targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                    if len(targets) == 1:
                        owner = self.object_key(module, targets[0])
                        if owner:
                            self.objects[owner]["dependencies"] = self.referenced_files(module, path, node.value)
                            self.objects[owner]["symbols"] = self.referenced_symbols(module, node.value)
                            self.objects[owner]["fallback"] = self.referenced_fallback_files(module, node.value)

    def referenced_files(self, module, path, node):
        files = set()
        local_function = False
        for value in ast.walk(node):
            if isinstance(value, (ast.Name, ast.Attribute)):
                name = self.expression(module, value)
                if name and name not in self.objects:
                    files.update(self.module_paths(name))
                    function = self.function_location(name)
                    if function and function["file"] == path:
                        local_function = True
        files.discard(path)
        if local_function:
            files.add(path)
        return sorted(files)

    def referenced_symbols(self, module, node):
        symbols = set()
        for value in ast.walk(node):
            if isinstance(value, (ast.Name, ast.Attribute)):
                name = self.symbol_graph.resolve(module, value, set(), {})
                if name in self.functions:
                    symbols.add(name)
                elif name is None and self.module_paths(self.expression(module, value) or ""):
                    path = self.modules.get(module, "")
                    self.issue("ambiguous_symbol_reference", path, value.lineno,
                               "A registration reference has a conditional or repeated binding; using module fallback.")
        return sorted(symbols)

    def referenced_fallback_files(self, module, node):
        files = set()
        def walk(value):
            if isinstance(value, (ast.Name, ast.Attribute)):
                name = self.expression(module, value)
                if name and not any(name == key or name.startswith(key + ".") for key in self.objects) and (name not in self.functions or self.symbol_graph.resolve(module, value, set(), {}) is None):
                    files.update(self.module_paths(name))
                return
            for child in ast.iter_child_nodes(value):
                walk(child)
        walk(node)
        return sorted(files)

    def symbol_fallback_files(self, roots):
        seen = set()
        queue = list(roots)
        files = set()
        while queue:
            name = queue.pop()
            if name in seen:
                continue
            seen.add(name)
            symbol = self.symbol_graph.symbols.get(name)
            if symbol:
                files.update(symbol["fallback_files"])
                queue.extend(symbol["references"])
        return sorted(files)

    def methods(self, call, action, path):
        if action in self.METHODS:
            return [action.upper()]
        node = self.argument(call, "methods")
        if node is None:
            return ["GET"]
        if not isinstance(node, (ast.List, ast.Tuple, ast.Set)):
            self.issue("dynamic_methods", path, call.lineno, "Route methods must be a literal collection.")
            return None
        methods = [self.literal(value) for value in node.elts]
        if not methods or any(value is None or value.lower() not in self.METHODS for value in methods):
            self.issue("dynamic_methods", path, call.lineno, "Route methods contain an unsupported or nonliteral value.")
            return None
        return sorted(set(value.upper() for value in methods))

    def declaration(self, module, path, call, handler):
        if not isinstance(call, ast.Call) or not isinstance(call.func, ast.Attribute):
            return False
        action = call.func.attr
        if action not in self.METHODS | {"api_route", "add_api_route"}:
            return False
        owner = self.object_key(module, call.func.value)
        if owner is None:
            self.issue("unresolved_route_owner", path, call.lineno, "A possible route registration has an unresolved application or router.")
            return True
        route_path = self.literal(self.argument(call, "path", 0))
        methods = self.methods(call, action, path)
        if route_path is None or (route_path and not route_path.startswith("/")):
            self.issue("dynamic_path", path, call.lineno, "Route paths must be literal path strings.")
            return True
        if methods is None:
            return True
        if any(keyword.arg is None for keyword in call.keywords) or any(isinstance(arg, ast.Starred) for arg in call.args):
            self.issue("dynamic_arguments", path, call.lineno, "Expanded route arguments cannot be resolved statically.")
        if handler is None:
            endpoint = self.argument(call, "endpoint", 1)
            name = self.expression(module, endpoint)
            handler = self.function_location(name)
            if handler is not None and self.symbol_graph.resolve(module, endpoint, set(), {}) != name:
                self.issue("ambiguous_handler_binding", path, call.lineno, "The endpoint reference has a conditional or repeated binding.")
            if handler is None:
                self.issue("unresolved_handler", path, call.lineno, "Route endpoint must resolve to a module-level function.")
                return True
        self.declarations[owner].append({"path": route_path, "methods": methods, "handler": handler,
                                         "registration": {"file": path, "line": call.lineno},
                                         "dependencies": self.referenced_files(module, path, call),
                                         "symbols": self.referenced_symbols(module, call),
                                         "fallback": self.referenced_fallback_files(module, call)})
        return True

    def function_location(self, name):
        return self.functions.get(name)

    def registration(self, module, path, call):
        if not isinstance(call.func, ast.Attribute):
            return
        owner = self.object_key(module, call.func.value)
        if call.func.attr == "include_router":
            child = self.object_key(module, self.argument(call, "router", 0))
            prefix = self.literal(self.argument(call, "prefix"), "")
            if owner is None or child is None or prefix is None or (prefix and (not prefix.startswith("/") or prefix.endswith("/"))):
                self.issue("dynamic_router", path, call.lineno, "Router inclusion must resolve both objects and a literal valid prefix.")
                return
            if any(keyword.arg is None for keyword in call.keywords):
                self.issue("dynamic_arguments", path, call.lineno, "Expanded router inclusion arguments cannot be resolved statically.")
            self.edges[owner].append({"child": child, "prefix": prefix, "file": path, "line": call.lineno,
                                     "dependencies": self.referenced_files(module, path, call),
                                         "symbols": self.referenced_symbols(module, call),
                                         "fallback": self.referenced_fallback_files(module, call)})
        elif call.func.attr in {"add_middleware", "add_exception_handler"} and owner:
            self.objects[owner]["shared"].append({"file": path, "dependencies": self.referenced_files(module, path, call),
                                         "symbols": self.referenced_symbols(module, call),
                                         "fallback": self.referenced_fallback_files(module, call)})
        elif call.func.attr in {"mount", "add_route", "add_websocket_route", "add_api_websocket_route", "route", "websocket", "websocket_route"} and owner:
            self.issue("unsupported_registration", path, call.lineno, "Mounted applications, Starlette routes, and WebSocket routes are outside this HTTP index.")

    def scan(self):
        self.bindings()
        self.imports()
        for path, (module, tree) in self.files.items():
            handled = set()
            bindings = {}
            for node in tree.body:
                if isinstance(node, (ast.Assign, ast.AnnAssign)):
                    targets = node.targets if isinstance(node, ast.Assign) else [node.target]
                    for target in targets:
                        if isinstance(target, ast.Name):
                            key = (module + "." if module else "") + target.id
                            bindings[key] = bindings.get(key, 0) + 1
                            if key in self.objects and bindings[key] > 1:
                                self.issue("rebound_object", path, node.lineno,
                                           "Application or router bindings are reassigned; runtime identity is ambiguous.")
                        elif isinstance(target, (ast.Attribute, ast.Subscript)):
                            value = target.value
                            if self.owned_expression(module, value):
                                self.issue("object_mutation", path, node.lineno,
                                           "Direct application or router attribute mutation is not modeled.")
                if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    handler = {"name": node.name, "file": path, "line": node.lineno,
                               "symbol": (module + "." if module else "") + node.name}
                    for decorator in node.decorator_list:
                        if self.declaration(module, path, decorator, handler):
                            handled.add(id(decorator))
                        elif isinstance(decorator, ast.Call) and isinstance(decorator.func, ast.Attribute):
                            owner = self.object_key(module, decorator.func.value)
                            if owner and decorator.func.attr in {"middleware", "exception_handler"}:
                                self.objects[owner]["shared"].append({"file": path, "dependencies": self.referenced_files(module, path, node),
                                                                        "symbols": sorted(set(self.referenced_symbols(module, decorator) + [handler["symbol"]])),
                                                                        "fallback": self.referenced_fallback_files(module, decorator)})
                                handled.add(id(decorator))
                            elif owner and decorator.func.attr in {"route", "websocket", "websocket_route", "on_event"}:
                                self.issue("unsupported_registration", path, decorator.lineno,
                                           "This application decorator is outside the supported HTTP registration model.")
                                handled.add(id(decorator))
                elif isinstance(node, ast.Expr) and isinstance(node.value, ast.Call):
                    call = node.value
                    if isinstance(call.func, ast.Attribute) and call.func.attr == "add_api_route":
                        self.declaration(module, path, call, None)
                        handled.add(id(call))
                    elif isinstance(call.func, ast.Attribute) and call.func.attr in {
                        "include_router", "add_middleware", "add_exception_handler", "mount",
                        "add_route", "add_websocket_route", "add_api_websocket_route", "websocket", "websocket_route", "route"
                    }:
                        self.registration(module, path, call)
                        handled.add(id(call))
            for node in ast.walk(tree):
                if not isinstance(node, ast.Call) or id(node) in handled:
                    continue
                name = self.expression(module, node.func)
                if name in {"fastapi.FastAPI", "fastapi.APIRouter", "fastapi.applications.FastAPI", "fastapi.routing.APIRouter"}:
                    # A constructor belongs to a known object only when its
                    # assignment is directly in the module body.
                    if not any(isinstance(stmt, (ast.Assign, ast.AnnAssign)) and stmt.value is node for stmt in tree.body):
                        self.issue("dynamic_factory", path, node.lineno, "Applications or routers constructed in factories or control flow are not resolved.")
                elif isinstance(node.func, ast.Attribute):
                    action = node.func.attr
                    owner = self.object_key(module, node.func.value)
                    if action in {"include_router", "add_api_route", "mount"} or (owner and action in self.METHODS | {"api_route"}):
                        self.issue("dynamic_registration", path, node.lineno, "Route registration inside control flow or functions is not resolved.")
                    elif owner and action in {"route", "add_route", "add_middleware", "add_exception_handler", "websocket", "on_event"}:
                        self.issue("dynamic_registration", path, node.lineno, "Application configuration inside control flow or functions is not resolved.")
                    elif self.owned_expression(module, node.func.value) and action in {"append", "extend", "insert", "clear", "remove", "pop", "update", "add", "__setitem__"}:
                        self.issue("object_mutation", path, node.lineno, "Application or router collection mutation is not modeled.")
                    elif name != "uvicorn.run" and (any(self.object_key(module, arg) for arg in node.args) or any(self.object_key(module, keyword.value) for keyword in node.keywords)):
                        self.issue("opaque_object_use", path, node.lineno,
                                   "Passing an application or router to an opaque helper may change registration.")
                elif name in {"builtins.eval", "builtins.exec"} or (isinstance(node.func, ast.Name) and node.func.id in {"eval", "exec"} and not self.symbol_graph.bindings.get(module, {}).get(node.func.id)):
                    self.issue("dynamic_execution", path, node.lineno, "Dynamic code can change the route or import graph.")
                elif any(self.object_key(module, arg) for arg in node.args) or any(self.object_key(module, keyword.value) for keyword in node.keywords):
                    self.issue("opaque_object_use", path, node.lineno,
                               "Passing an application or router to an opaque helper may change registration.")
        roots = sorted(key for key, value in self.objects.items() if value["kind"] == "app")
        requested = self.request.get("entrypoint", "")
        if requested:
            module, separator, variable = requested.partition(":")
            key = module + "." + variable
            if not separator or key not in roots:
                self.issue("entrypoint_unresolved", "", 0, "Requested entrypoint does not resolve to a module-level FastAPI instance.")
                roots = []
            else:
                roots = [key]
        elif len(roots) != 1:
            self.issue("entrypoint_ambiguous" if roots else "framework_unresolved", "", 0,
                       "Select module:variable with --entrypoint when there is not exactly one static FastAPI instance.")
            roots = []
        entrypoint = ""
        if roots:
            module, _, variable = roots[0].rpartition(".")
            entrypoint = module + ":" + variable
            root_file = self.objects[roots[0]]["file"]
            self.loaded = self.reachable(root_file, self.load_dependencies)
            possible = self.reachable(root_file, self.dependencies) - self.loaded
            registrations = {}
            for rows in self.declarations.values():
                for row in rows:
                    registrations[row["registration"]["file"]] = row["registration"]["line"]
            for rows in self.edges.values():
                for row in rows:
                    registrations[row["file"]] = row["line"]
            for obj in self.objects.values():
                for shared in obj["shared"]:
                    registrations[shared["file"]] = 1
            for path in sorted(possible):
                if path in registrations:
                    self.issue("conditional_route_import", path, registrations[path],
                               "A function or conditional import may register routes or shared configuration when executed.")
            self.expand(roots[0], "", [], [], [], [], [])
        # Duplicate method/path registrations have order-sensitive dispatch.
        grouped = {}
        for route in self.routes:
            key = (route["method"], route["path"])
            if key in grouped:
                self.issue("duplicate_route", route["registration"]["file"], route["registration"]["line"],
                           "Duplicate method/path registrations have ambiguous dispatch in this static index.")
            else:
                grouped[key] = route
        result = {"entrypoint": entrypoint, "routes": sorted(grouped.values(), key=lambda row: (row["path"], row["method"])),
                "dependencies": self.dependencies,
                "issues": sorted(self.issues, key=lambda row: (row["file"], row["line"], row["code"]))}
        result.update(self.symbol_graph.result())
        return result

    @staticmethod
    def reachable(start, graph):
        seen = {start}
        queue = [start]
        while queue:
            path = queue.pop()
            for child in graph.get(path, []):
                if child not in seen:
                    seen.add(child)
                    queue.append(child)
        return seen

    def expand(self, owner, prefix, context, dependency_files, dependency_symbols, fallback_files, stack):
        if owner in stack:
            obj = self.objects[owner]
            self.issue("router_cycle", obj["file"], obj["line"], "Router inclusion contains a cycle.")
            return
        if len(stack) >= self.limits["depth"]:
            raise ValueError("route impact router depth limit exceeded")
        obj = self.objects[owner]
        if obj["prefix"] is None:
            return
        prefix += obj["prefix"]
        context = sorted(set(context + [obj["file"]]))
        dependency_files = sorted(set(dependency_files + obj["dependencies"]))
        dependency_symbols = sorted(set(dependency_symbols + obj["symbols"]))
        fallback_files = sorted(set(fallback_files + obj["fallback"]))
        for shared in obj["shared"]:
            if shared["file"] in self.loaded:
                context = sorted(set(context + [shared["file"]]))
                dependency_files = sorted(set(dependency_files + shared["dependencies"]))
                dependency_symbols = sorted(set(dependency_symbols + shared["symbols"]))
                fallback_files = sorted(set(fallback_files + shared["fallback"]))
        for declaration in self.declarations[owner]:
            if declaration["registration"]["file"] not in self.loaded:
                continue
            handler = declaration["handler"]
            path = prefix + declaration["path"]
            if not path:
                self.issue("invalid_path", obj["file"], obj["line"], "An unprefixed route has an empty path.")
                continue
            for method in declaration["methods"]:
                if len(self.routes) >= self.limits["routes"]:
                    raise ValueError("route impact route limit exceeded")
                self.routes.append({"method": method, "path": path, "handler": handler["name"],
                                    "source": {"file": handler["file"], "line": handler["line"]},
                                    "handler_symbol": handler["symbol"],
                                    "registration": declaration["registration"],
                                    "context_files": sorted(set(context + [declaration["registration"]["file"]])),
                                    "dependency_files": sorted(set(dependency_files + declaration["dependencies"])),
                                    "dependency_symbols": sorted(set(dependency_symbols + declaration["symbols"])),
                                    "fallback_files": sorted(set(fallback_files + declaration["fallback"] + self.symbol_fallback_files([handler["symbol"]] + dependency_symbols + declaration["symbols"])))})
        for edge in self.edges[owner]:
            if edge["file"] not in self.loaded:
                continue
            child = self.objects[edge["child"]]
            later = [row["registration"]["line"] for row in self.declarations[edge["child"]]
                     if row["registration"]["file"] == edge["file"]]
            later += [row["line"] for row in self.edges[edge["child"]] if row["file"] == edge["file"]]
            if child["file"] == edge["file"] and (child["line"] > edge["line"] or any(line > edge["line"] for line in later)):
                self.issue("registration_order", edge["file"], edge["line"],
                           "Router inclusion precedes child construction or registration; runtime dispatch cannot be inferred.")
            self.expand(edge["child"], prefix + edge["prefix"], context + [edge["file"]],
                        dependency_files + edge["dependencies"], dependency_symbols + edge["symbols"], fallback_files + edge["fallback"], stack + [owner])


def main():
    try:
        result = Indexer(json.load(sys.stdin)).scan()
        json.dump(result, sys.stdout, sort_keys=True, separators=(",", ":"))
    except (ValueError, RecursionError, MemoryError):
        # Do not echo parser exceptions: they may contain customer source.
        sys.stderr.write("Static analysis exceeded a bound or could not decode its input.\n")
        sys.exit(1)


if __name__ == "__main__":
    main()
