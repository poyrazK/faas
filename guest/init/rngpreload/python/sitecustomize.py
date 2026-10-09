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
        return "ok"

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

    search = [p for p in sys.path if os.path.abspath(p or os.curdir) != here]
    spec = importlib.machinery.PathFinder.find_spec("sitecustomize", search)
    if spec is None or spec.loader is None or not spec.origin:
        return
    if os.path.abspath(spec.origin) == os.path.abspath(__file__):
        return
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)


try:
    _gregale_restore_reseed()
except Exception:  # noqa: BLE001 - the preload must never stop the app starting
    pass
_chain_customer_sitecustomize()
