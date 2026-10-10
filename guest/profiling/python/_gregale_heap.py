"""tracemalloc snapshots encoded as gzip pprof heap profiles (ADR-967).

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
MAX_SOURCE_BYTES = 1 << 20
_SKIP = (os.path.abspath(__file__), os.path.abspath(tracemalloc.__file__))


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


def start():
    if not tracemalloc.is_tracing():
        tracemalloc.start(MAX_FRAMES)


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
