"""tracemalloc snapshots encoded as gzip pprof heap profiles (ADR-1155).

tracemalloc records allocations made after it starts. A capture therefore
reports memory allocated during its window that is still live at the end,
which is the signature of a leak. Only the standard library is used.
"""
import ast
import gzip
import os
import time
import tracemalloc

MAX_SAMPLES = 5000
MAX_FRAMES = 32
# Captures taken because memory runs low record shorter tracebacks: tracing
# costs memory per traced block and per distinct traceback.
LOW_MEMORY_FRAMES = 8
# Tracing stops, without a snapshot, once available memory falls below the
# larger of these: a heap capture must never cause the OOM kill it explains.
FLOOR_BYTES = 16 << 20
FLOOR_PERCENT = 5
# A snapshot copies every trace into Python objects, several times the
# tracer's own storage; it is taken only with this much room to spare.
SNAPSHOT_COST_FACTOR = 4
SNAPSHOT_HEADROOM_BYTES = 16 << 20
MAX_SOURCE_BYTES = 1 << 20
# Allocations made by the profiler itself, including the collector's control
# loop in sitecustomize (urllib, json, http.client), are not the app's.
_SKIP = (os.path.abspath(__file__), os.path.abspath(tracemalloc.__file__),
         os.path.join(os.path.dirname(os.path.abspath(__file__)), "sitecustomize.py"),
         os.path.join(os.path.dirname(os.path.abspath(__file__)), "_gregale_stacks.py"))


def _varint(value):
    out = bytearray()
    value &= (1 << 64) - 1
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            out.append(byte | 0x80)
        else:
            out.append(byte)
            return bytes(out)


def _field_varint(number, value):
    return _varint(number << 3) + _varint(value)


def _field_bytes(number, data):
    return _varint((number << 3) | 2) + _varint(len(data)) + data


def _packed(number, values):
    return _field_bytes(number, b"".join(_varint(v) for v in values))


class _Strings:
    def __init__(self):
        self.table = [""]
        self.index = {"": 0}

    def __call__(self, value):
        if value not in self.index:
            self.index[value] = len(self.table)
            self.table.append(value)
        return self.index[value]


class _FunctionNames:
    """Maps a file and line to the innermost enclosing def or class."""

    def __init__(self):
        self.files = {}

    def __call__(self, filename, line):
        scopes = self.files.get(filename)
        if scopes is None:
            scopes = self._scopes(filename)
            self.files[filename] = scopes
        best = None
        for start, end, name in scopes:
            if start <= line <= end and (best is None or start >= best[0]):
                best = (start, end, name)
        return best[2] if best else "<module>"

    @staticmethod
    def _scopes(filename):
        try:
            if os.path.getsize(filename) > MAX_SOURCE_BYTES:
                return []
            with open(filename, "rb") as source:
                tree = ast.parse(source.read())
        except Exception:
            return []
        scopes = []

        def walk(node, prefix):
            for child in ast.iter_child_nodes(node):
                if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                    name = prefix + child.name
                    scopes.append((child.lineno, getattr(child, "end_lineno", child.lineno) or child.lineno, name))
                    walk(child, name + ".")
                else:
                    walk(child, prefix)

        walk(tree, "")
        return scopes


def start(frames=MAX_FRAMES):
    if not tracemalloc.is_tracing():
        tracemalloc.start(frames)


def _read_int(path):
    try:
        with open(path) as f:
            value = f.read().strip()
    except OSError:
        return None
    return None if value == "max" else int(value)


def memory():
    """Returns (available, total) bytes for this process, or (None, None).

    The guest's MemAvailable, narrowed by the process's cgroup v2 limit when
    it has one.
    """
    available = total = None
    try:
        with open("/proc/meminfo") as f:
            for line in f:
                key, _, rest = line.partition(":")
                if key in ("MemAvailable", "MemTotal"):
                    value = int(rest.split()[0]) * 1024
                    if key == "MemAvailable":
                        available = value
                    else:
                        total = value
    except (OSError, ValueError, IndexError):
        return None, None
    try:
        with open("/proc/self/cgroup") as f:
            path = next((line.split("::", 1)[1].strip() for line in f if line.startswith("0::")), None)
        if path is not None:
            base = "/sys/fs/cgroup" + path.rstrip("/")
            limit, used = _read_int(base + "/memory.max"), _read_int(base + "/memory.current")
            if limit is not None and used is not None:
                available = min(available, max(limit - used, 0)) if available is not None else max(limit - used, 0)
                total = min(total, limit) if total is not None else limit
    except (OSError, ValueError):
        pass
    return available, total


def low_memory(available=None, total=None):
    """Reports whether tracing must stop to protect the process."""
    if available is None:
        available, total = memory()
    if available is None:
        return False
    floor = max(FLOOR_BYTES, (total or 0) * FLOOR_PERCENT // 100)
    return available < floor


def snapshot_affordable(available=None, traced=None):
    """Reports whether a snapshot fits in the memory left."""
    if available is None:
        available, _ = memory()
    if available is None:
        return True
    if traced is None:
        traced = tracemalloc.get_tracemalloc_memory()
    return available > traced * SNAPSHOT_COST_FACTOR + SNAPSHOT_HEADROOM_BYTES


def stop():
    if tracemalloc.is_tracing():
        tracemalloc.stop()


_NAMES = _FunctionNames()


def snapshot_pprof(started_ns, stop=False):
    """Returns a gzip pprof profile of traced live allocations, or None.

    With stop, tracing ends right after the snapshot so encoding is not traced.
    """
    if not tracemalloc.is_tracing():
        return None
    if not snapshot_affordable():
        # No room to copy the traces: give up this profile rather than the
        # process. Stopping frees the tracer's own memory too.
        if stop:
            tracemalloc.stop()
        return None
    snapshot = tracemalloc.take_snapshot()
    if stop:
        tracemalloc.stop()
    # Snapshot.filter_traces matches every trace with fnmatch (seconds for a
    # few thousand traces); excluding the profiler's own frames from the
    # grouped statistics is equivalent and cheap.
    stats = [s for s in snapshot.statistics("traceback")
             if not any(frame.filename in _SKIP for frame in s.traceback)][:MAX_SAMPLES]
    strings, names = _Strings(), _NAMES
    functions, locations, samples = {}, {}, []
    for stat in stats:
        location_ids = []
        # Frames are oldest first; pprof lists the leaf first.
        for frame in reversed(list(stat.traceback)):
            key = (frame.filename, frame.lineno)
            if key not in locations:
                fname = (names(frame.filename, frame.lineno), frame.filename)
                if fname not in functions:
                    functions[fname] = len(functions) + 1
                locations[key] = (len(locations) + 1, functions[fname], frame.lineno)
            location_ids.append(locations[key][0])
        samples.append(_packed(1, location_ids) + _packed(2, [stat.count, stat.size]))
    now = time.time_ns()
    out = bytearray()
    for kind, unit in (("inuse_objects", "count"), ("inuse_space", "bytes")):
        out += _field_bytes(1, _field_varint(1, strings(kind)) + _field_varint(2, strings(unit)))
    for sample in samples:
        out += _field_bytes(2, sample)
    for location_id, function_id, line in locations.values():
        out += _field_bytes(4, _field_varint(1, location_id) + _field_bytes(4, _field_varint(1, function_id) + _field_varint(2, line)))
    period_type = _field_varint(1, strings("space")) + _field_varint(2, strings("bytes"))
    for (name, filename), function_id in functions.items():
        out += _field_bytes(5, _field_varint(1, function_id) + _field_varint(2, strings(name)) + _field_varint(4, strings(filename)))
    for value in strings.table:
        out += _field_bytes(6, value.encode("utf-8", "replace"))
    out += _field_varint(9, started_ns) + _field_varint(10, max(now - started_ns, 1))
    out += _field_bytes(11, period_type)
    return gzip.compress(bytes(out))
