"""Opt-in sampled CPU profiling for managed Python processes (ADR-819)."""
import os


def _load_customer_sitecustomize():
    # Python imports only the first sitecustomize on sys.path. Preserve the
    # application's startup hook when the platform bootstrap is prepended.
    import importlib.machinery
    import importlib.util
    import sys

    here = os.path.realpath(os.path.dirname(__file__))
    paths = [p for p in sys.path if os.path.realpath(p or os.getcwd()) != here]
    spec = importlib.machinery.PathFinder.find_spec("sitecustomize", paths)
    if spec is not None and spec.origin is not None:
        customer = importlib.util.spec_from_file_location("_gregale_customer_sitecustomize", spec.origin)
        if customer is not None and customer.loader is not None:
            module = importlib.util.module_from_spec(customer)
            sys.modules[customer.name] = module
            customer.loader.exec_module(module)


_load_customer_sitecustomize()

if os.getenv("FAAS_PROFILING_ENABLED") == "1":
    import json
    import threading
    import time
    import urllib.request

    _lock = threading.RLock()
    _state = {"profiler": None, "epoch": "", "running": False}

    def _stop():
        if _state["running"]:
            _state["profiler"].shutdown()
            _state["running"] = False

    def _control_loop():
        endpoint = os.getenv("FAAS_PROFILING_ENDPOINT", "http://127.0.0.1:9191")
        while True:
            try:
                with _lock:
                    with urllib.request.urlopen(endpoint + "/control?pid=" + str(os.getpid()), timeout=0.2) as response:
                        config = json.loads(response.read(4096))
                    if _state["running"] and (config["suspended"] or config["epoch"] != _state["epoch"] or not config["enabled"]):
                        _stop()
                    if config["suspended"]:
                        req = urllib.request.Request(endpoint + "/control/ack?pid=" + str(os.getpid()) + "&epoch=" + config["epoch"], method="POST")
                        with urllib.request.urlopen(req, timeout=0.2):
                            pass
                    elif config["enabled"] and not _state["running"]:
                        import pyroscope
                        pyroscope.configure(application_name="gregale", server_address=endpoint,
                                            sample_rate=100, oncpu=True, gil_only=True,
                                            cpu_enabled=True, mem_enabled=False,
                                            upload_interval=config["window_seconds"],
                                            tags={"gregale_epoch": config["epoch"], "gregale_process": str(os.getpid())})
                        _state.update(profiler=pyroscope, epoch=config["epoch"], running=True)
            except Exception:
                pass
            time.sleep(0.1)

    def _before_fork():
        # Never copy a running native agent or its worker locks into a child.
        _lock.acquire()
        try:
            _stop()
        except Exception:
            # Profiling must never prevent an application fork. Keep the
            # lock held until the matching parent/child callback runs.
            _state["running"] = False

    def _after_parent():
        _lock.release()

    def _after_child():
        global _lock
        _lock = threading.RLock()
        _state.update(profiler=None, epoch="", running=False)
        threading.Thread(target=_control_loop, name="gregale-profiling-control", daemon=True).start()

    if hasattr(os, "register_at_fork"):
        os.register_at_fork(before=_before_fork, after_in_parent=_after_parent, after_in_child=_after_child)
    threading.Thread(target=_control_loop, name="gregale-profiling-control", daemon=True).start()
