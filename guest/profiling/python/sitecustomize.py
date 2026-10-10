"""Sampled CPU and heap profiling for managed Python processes (ADR-819, ADR-967)."""
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
    import urllib.parse
    import urllib.request

    _lock = threading.RLock()
    _state = {"profiler": None, "epoch": "", "running": False, "capture": None,
              "heap": False, "heap_from": 0}

    def _kinds(config):
        return config.get("kinds") or ["cpu"]

    def _upload_heap(endpoint, epoch, started_ns, stop=False):
        import _gregale_heap
        body = _gregale_heap.snapshot_pprof(started_ns, stop)
        if not body:
            return
        name = "gregale{gregale_epoch=" + epoch + ",gregale_process=" + str(os.getpid()) + "}"
        query = urllib.parse.urlencode({"kind": "heap", "name": name, "from": started_ns // 10**9,
                                        "until": time.time_ns() // 10**9})
        req = urllib.request.Request(endpoint + "/ingest?" + query, data=body, method="POST",
                                     headers={"Content-Type": "application/octet-stream"})
        try:
            with urllib.request.urlopen(req, timeout=2):
                pass
        except Exception:
            pass

    def _start_cpu(endpoint, config):
        import pyroscope
        pyroscope.configure(application_name="gregale", server_address=endpoint,
                            sample_rate=100, oncpu=True, gil_only=True,
                            cpu_enabled=True, mem_enabled=False,
                            upload_interval=config["window_seconds"],
                            tags={"gregale_epoch": config["epoch"], "gregale_process": str(os.getpid())})
        _state.update(profiler=pyroscope, running=True)

    def _start_heap():
        import _gregale_heap
        _gregale_heap.start()
        _state.update(heap=True, heap_from=time.time_ns())

    def _stop_heap(endpoint, flush):
        if not _state["heap"]:
            return
        import _gregale_heap
        if flush:
            _upload_heap(endpoint, _state["epoch"], _state["heap_from"], stop=True)
        _gregale_heap.stop()
        _state["heap"] = False

    def _stop(endpoint=None, flush=False):
        if _state["running"]:
            _state["profiler"].shutdown()
            _state["running"] = False
        _stop_heap(endpoint, flush and endpoint is not None)
        _state["capture"] = None

    def _tick(endpoint):
        with urllib.request.urlopen(endpoint + "/control?pid=" + str(os.getpid()), timeout=0.2) as response:
            config = json.loads(response.read(4096))
        capture = _state["capture"]
        same_epoch = config["epoch"] == _state["epoch"]
        if (_state["running"] or _state["heap"]) and (
                config["suspended"] or not same_epoch or not config["enabled"]
                or bool(config.get("capture")) != (capture is not None)):
            # The window ends in its own epoch: flush. A new epoch is a
            # checkpoint or restore, whose samples are discarded.
            _stop(endpoint, flush=same_epoch and not config["suspended"])
        if config["suspended"]:
            req = urllib.request.Request(endpoint + "/control/ack?pid=" + str(os.getpid()) + "&epoch=" + config["epoch"], method="POST")
            with urllib.request.urlopen(req, timeout=0.2):
                pass
        elif config["enabled"] and not _state["running"] and not _state["heap"] and not same_epoch:
            kinds = _kinds(config)
            _state["epoch"] = config["epoch"]
            if config.get("capture"):
                _state["capture"] = {"epoch": config["epoch"]}
            if "cpu" in kinds:
                _start_cpu(endpoint, config)
            if "heap" in kinds:
                _start_heap()
        elif _state["heap"] and capture is None and time.time_ns() - _state["heap_from"] >= config["window_seconds"] * 10**9:
            # Continuous heap: report traced live allocations once per window.
            started, _state["heap_from"] = _state["heap_from"], time.time_ns()
            _upload_heap(endpoint, _state["epoch"], started)
        return config["enabled"] or _state["running"] or _state["heap"]

    def _control_loop():
        endpoint = os.getenv("FAAS_PROFILING_ENDPOINT", "http://127.0.0.1:9191")
        while True:
            active = False
            try:
                with _lock:
                    active = _tick(endpoint)
            except Exception:
                pass
            # Dormant collectors poll slowly; an armed capture starts within 1s.
            time.sleep(0.1 if active else 1.0)

    def _before_fork():
        # Never copy a running native agent or its worker locks into a child.
        _lock.acquire()
        # Clearing the epoch lets the parent restart collection after fork.
        _state["epoch"] = ""
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
        _state.update(profiler=None, epoch="", running=False, capture=None, heap=False, heap_from=0)
        threading.Thread(target=_control_loop, name="gregale-profiling-control", daemon=True).start()

    if hasattr(os, "register_at_fork"):
        os.register_at_fork(before=_before_fork, after_in_parent=_after_parent, after_in_child=_after_child)
    threading.Thread(target=_control_loop, name="gregale-profiling-control", daemon=True).start()
