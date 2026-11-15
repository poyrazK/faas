# Gregale restore reseed preload (GHSA-24j2-p895-mwc9, ADR-687).
#
# guest-init puts this directory first on PYTHONPATH. A snapshot restores a
# Python process with the random state it held at capture, so every restore of
# one snapshot would replay the same random.random() sequence and, once the ssl
# module has drawn from OpenSSL, the same TLS randomness. On resume guest-init
# sends each registered process a fresh nonce; this module reseeds `random`,
# OpenSSL (ssl.RAND_add) and numpy's legacy global generator, then replies
# "ok". guest-init waits for that reply before the instance serves traffic.
#
# os.urandom, secrets and uuid.uuid4 read the kernel on every call and need
# nothing here. A customer sitecustomize further down sys.path still runs.
import os
import sys


def _gregale_restore_reseed():
    path = os.environ.get("GREGALE_RESEED_SOCKET")
    if not path or os.environ.get("GREGALE_RESTORE_RESEED", "").lower() == "off":
        return
    import socket
    import threading

    def reseed(nonce_hex):
        nonce = bytes.fromhex(nonce_hex)
        if len(nonce) < 16:
            return "err bad_nonce"
        fresh = bytes(a ^ b for a, b in zip(os.urandom(len(nonce)), nonce))
        modules = sys.modules
        if "random" in modules:
            modules["random"].seed(fresh)
        if "ssl" in modules or "_ssl" in modules:
            import ssl

            ssl.RAND_add(fresh, float(len(fresh)))
        numpy = modules.get("numpy")
        if numpy is not None:
            legacy_seed = getattr(getattr(numpy, "random", None), "seed", None)
            if callable(legacy_seed):
                legacy_seed()
        _reset_id_libraries(modules)
        return "ok"

    def _reset_id_libraries(modules):
        # ADR-20261110170000: pymongo's bson.ObjectId keeps a 5-byte
        # per-process random value and a counter; it regenerates them only
        # when the PID changes, which a restore keeps. Best effort.
        objectid = modules.get("bson.objectid")
        cls = getattr(objectid, "ObjectId", None)
        if cls is None:
            return
        try:
            import random as _r

            if hasattr(cls, "_ObjectId__random"):
                cls._ObjectId__random = os.urandom(5)
            if hasattr(cls, "_inc"):
                cls._inc = _r.SystemRandom().randint(0, 0xFFFFFF)
        except Exception:  # noqa: BLE001 - a version this does not know
            pass

    def serve():
        try:
            conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            conn.connect(path)
            conn.sendall(("hello python %d\n" % os.getpid()).encode())
            stream = conn.makefile("rb")
        except OSError:
            return
        for raw in stream:
            line = raw.decode("ascii", "replace").strip()
            if not line.startswith("reseed "):
                continue
            try:
                reply = reseed(line[len("reseed "):].strip())
            except Exception as err:  # noqa: BLE001 - any failure must fail the barrier
                reply = "err " + type(err).__name__
            try:
                conn.sendall((reply + "\n").encode())
            except OSError:
                return

    def start():
        threading.Thread(target=serve, name="gregale-restore-reseed", daemon=True).start()

    start()
    # A pre-forking server (gunicorn, uwsgi) forks after this module ran; the
    # listener thread does not survive fork, so every child registers itself.
    if hasattr(os, "register_at_fork"):
        os.register_at_fork(after_in_child=start)


def _chain_customer_sitecustomize():
    here = os.path.dirname(os.path.abspath(__file__))
    import importlib.machinery
    import importlib.util

    # Search only entries after this directory: a stacked platform bootstrap
    # earlier on sys.path would otherwise be found again and recurse.
    resolved = [os.path.abspath(p or os.curdir) for p in sys.path]
    start = resolved.index(here) + 1 if here in resolved else 0
    search = [p for p, r in zip(sys.path[start:], resolved[start:]) if r != here]
    spec = importlib.machinery.PathFinder.find_spec("sitecustomize", search)
    if spec is None or spec.loader is None or not spec.origin:
        return
    if os.path.abspath(spec.origin) == os.path.abspath(__file__):
        return
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)


# A gunicorn master kills any worker whose heartbeat file is older than
# --timeout (30 s by default), measured on time.monotonic(). A restore advances
# the guest's monotonic clock by the time the snapshot spent parked
# (ADR-20261109120000), so on resume the master's 1 s select returns at once
# and murder_workers sees every worker silent for the whole park: it kills one
# ("WORKER TIMEOUT") and boots a replacement on every wake, and the
# replacement's imports compete with the resume hook for the guest's vCPUs.
#
# The master checks its workers about once a second. When more than
# _GUNICORN_FROZEN_GAP seconds pass between two checks the master itself did
# not run, so the gap says nothing about its workers: refresh every worker's
# heartbeat and skip that one check. A worker that is really stuck is still
# killed --timeout seconds later.
_GUNICORN_FROZEN_GAP = 5.0


def _patch_gunicorn_arbiter(module):
    arbiter = getattr(module, "Arbiter", None)
    original = getattr(arbiter, "murder_workers", None)
    if original is None or getattr(original, "_gregale_freeze_guard", False):
        return
    import time

    def murder_workers(self, *args, **kwargs):
        now = time.monotonic()
        last = getattr(self, "_gregale_last_worker_check", None)
        self._gregale_last_worker_check = now
        if last is not None and now - last > _GUNICORN_FROZEN_GAP:
            for worker in list(getattr(self, "WORKERS", {}).values()):
                try:
                    worker.tmp.notify()
                except Exception:  # noqa: BLE001 - a worker that just exited
                    pass
            return None
        return original(self, *args, **kwargs)

    murder_workers._gregale_freeze_guard = True
    arbiter.murder_workers = murder_workers


def _gregale_gunicorn_freeze_guard():
    if os.environ.get("GREGALE_GUNICORN_FREEZE_GUARD", "").lower() == "off":
        return
    module = sys.modules.get("gunicorn.arbiter")
    if module is not None:
        _patch_gunicorn_arbiter(module)
        return
    import importlib.abc
    import importlib.machinery

    class _Loader(importlib.abc.Loader):
        def __init__(self, inner):
            self._inner = inner

        def create_module(self, spec):
            return self._inner.create_module(spec)

        def exec_module(self, module):
            self._inner.exec_module(module)
            try:
                _patch_gunicorn_arbiter(module)
            except Exception:  # noqa: BLE001 - a gunicorn this does not know
                pass

    class _Finder(importlib.abc.MetaPathFinder):
        def find_spec(self, fullname, path, target=None):
            if fullname != "gunicorn.arbiter":
                return None
            spec = importlib.machinery.PathFinder.find_spec(fullname, path)
            if spec is None or spec.loader is None or not hasattr(spec.loader, "exec_module"):
                return spec
            spec.loader = _Loader(spec.loader)
            return spec

    sys.meta_path.insert(0, _Finder())


try:
    _gregale_restore_reseed()
except Exception:  # noqa: BLE001 - the preload must never stop the app starting
    pass
try:
    _gregale_gunicorn_freeze_guard()
except Exception:  # noqa: BLE001 - the preload must never stop the app starting
    pass
_chain_customer_sitecustomize()
