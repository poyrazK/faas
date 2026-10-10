"""Opt-in request tracing for managed Python processes (ADR-934)."""
import os


def _load_customer_sitecustomize():
    # Python imports only the first sitecustomize on sys.path. Preserve the
    # application's (or another platform bootstrap's) startup hook when this
    # directory is prepended.
    import importlib.machinery
    import importlib.util
    import sys

    # Search only entries after this directory. Stacked platform bootstraps
    # (restore reseed, profiling, tracing) each chain to the next; excluding
    # only our own directory would find an earlier bootstrap again and recurse.
    here = os.path.realpath(os.path.dirname(__file__))
    resolved = [os.path.realpath(p or os.getcwd()) for p in sys.path]
    start = resolved.index(here) + 1 if here in resolved else 0
    paths = [p for p, r in zip(sys.path[start:], resolved[start:]) if r != here]
    spec = importlib.machinery.PathFinder.find_spec("sitecustomize", paths)
    if spec is not None and spec.origin is not None:
        customer = importlib.util.spec_from_file_location("_gregale_tracing_next_sitecustomize", spec.origin)
        if customer is not None and customer.loader is not None:
            module = importlib.util.module_from_spec(customer)
            sys.modules[customer.name] = module
            customer.loader.exec_module(module)


def _app_owns_sdk():
    # An application that ships its own SDK configures tracing itself; a
    # second, platform tracer provider would conflict with it.
    import importlib.util

    try:
        return importlib.util.find_spec("opentelemetry.sdk") is not None
    except (ImportError, ValueError):
        return False


_load_customer_sitecustomize()

if os.getenv("FAAS_TRACING_ENABLED") == "1" and not _app_owns_sdk():
    try:
        import sys

        # OpenTelemetry is a namespace package, so mixing versions across
        # sys.path breaks imports. The platform's OpenTelemetry packages (lib)
        # are prepended as one coherent set; their third-party dependencies
        # (deps) are appended so the application's versions of protobuf,
        # requests, wrapt and friends always win. An incompatible combination
        # only disables tracing.
        _root = os.path.dirname(os.path.realpath(__file__))
        _lib, _deps = os.path.join(_root, "lib"), os.path.join(_root, "deps")
        if os.path.isdir(_lib) and _lib not in sys.path:
            sys.path.insert(0, _lib)
        if os.path.isdir(_deps) and _deps not in sys.path:
            sys.path.append(_deps)
        from opentelemetry.instrumentation.auto_instrumentation import initialize

        initialize()
    except Exception:  # noqa: BLE001 - diagnostics never prevent serving
        pass
