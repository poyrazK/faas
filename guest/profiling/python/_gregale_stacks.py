"""Thread and asyncio task stacks encoded as gzip pprof profiles (ADR-1155).

A stack snapshot shows where every thread, and every pending asyncio task,
is waiting right now: the answer to "why is this request stuck". Identical
stacks are counted together. Only the standard library is used.
"""
import asyncio
import gzip
import sys
import threading
import time

from _gregale_heap import _Strings, _field_bytes, _field_varint, _packed

MAX_SAMPLES = 2000
MAX_FRAMES = 64
# The collector's own control thread is never the subject of a snapshot.
_OWN_THREADS = frozenset({"gregale-profiling-control"})


def _name(code):
    return getattr(code, "co_qualname", code.co_name)


def _thread_frames(frame):
    """Leaf-first frames of a running thread."""
    frames = []
    while frame is not None and len(frames) < MAX_FRAMES:
        frames.append((frame.f_code.co_filename, frame.f_lineno, _name(frame.f_code)))
        frame = frame.f_back
    return frames


def _running_loops(frames):
    """Event loops found in threads' run_forever frames."""
    loops = []
    for frame in frames.values():
        while frame is not None:
            if frame.f_code.co_name == "run_forever":
                loop = frame.f_locals.get("self")
                if isinstance(loop, asyncio.AbstractEventLoop) and loop not in loops:
                    loops.append(loop)
            frame = frame.f_back
    return loops


def _pending_tasks(frames):
    # asyncio has no "every loop" API, so each running loop is found through
    # the run_forever frame of the thread running it. Best effort: a failed
    # read only drops the task section.
    tasks = []
    for loop in _running_loops(frames):
        try:
            tasks.extend(t for t in asyncio.all_tasks(loop) if not t.done())
        except Exception:
            pass
    return tasks


def _task_frames(task):
    """Leaf-first frames of a suspended coroutine."""
    try:
        stack = task.get_stack(limit=MAX_FRAMES)  # oldest first
    except Exception:
        return []
    return [(f.f_code.co_filename, f.f_lineno, _name(f.f_code)) for f in reversed(stack)]


def snapshot_pprof():
    """Returns a gzip pprof profile of thread and task stacks, or None."""
    names = {t.ident: t.name for t in threading.enumerate()}
    me = threading.get_ident()
    counts = {}

    def add(root, frames):
        key = tuple(frames) + ((root,),)
        counts[key] = counts.get(key, 0) + 1

    frames = {ident: frame for ident, frame in sys._current_frames().items()
              if ident != me and names.get(ident) not in _OWN_THREADS}
    for frame in frames.values():
        add("<thread>", _thread_frames(frame))
    for task in _pending_tasks(frames):
        frames = _task_frames(task)
        if frames:
            add("<asyncio task>", frames)
    if not counts:
        return None
    strings = _Strings()
    functions, locations, samples = {}, {}, []
    for key, count in list(counts.items())[:MAX_SAMPLES]:
        location_ids = []
        for frame in key:
            if len(frame) == 1:  # pseudo root
                filename, line, name = "", 0, frame[0]
            else:
                filename, line, name = frame
            if (filename, line, name) not in locations:
                fkey = (name, filename)
                if fkey not in functions:
                    functions[fkey] = len(functions) + 1
                locations[(filename, line, name)] = (len(locations) + 1, functions[fkey], line)
            location_ids.append(locations[(filename, line, name)][0])
        samples.append(_packed(1, location_ids) + _packed(2, [count]))
    out = bytearray()
    out += _field_bytes(1, _field_varint(1, strings("threads")) + _field_varint(2, strings("count")))
    for sample in samples:
        out += _field_bytes(2, sample)
    for location_id, function_id, line in locations.values():
        out += _field_bytes(4, _field_varint(1, location_id) + _field_bytes(4, _field_varint(1, function_id) + _field_varint(2, line)))
    period_type = _field_varint(1, strings("threads")) + _field_varint(2, strings("count"))
    for (name, filename), function_id in functions.items():
        out += _field_bytes(5, _field_varint(1, function_id) + _field_varint(2, strings(name)) + _field_varint(4, strings(filename)))
    for value in strings.table:
        out += _field_bytes(6, value.encode("utf-8", "replace"))
    out += _field_varint(9, time.time_ns())
    out += _field_bytes(11, period_type)
    return gzip.compress(bytes(out))
