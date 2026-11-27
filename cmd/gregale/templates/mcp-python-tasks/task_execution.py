"""Execution limits and retry policy shared by web, workers and observers."""

import random
import threading
import time

TASK_LEASE_SECONDS = 30


class TaskLeaseDeadline:
    """Cancel execution when renewal cannot prove a live lease in time."""

    def __init__(self, on_expired, started_at=None, *, clock=None, timer_factory=None):
        self._clock = clock or time.monotonic
        self._timer_factory = timer_factory or threading.Timer
        self._on_expired = on_expired
        self._lock = threading.Lock()
        self._closed = False
        self._timer = None
        self._deadline = None
        self.renew(self._clock() if started_at is None else started_at)

    def _arm_locked(self, remaining):
        if self._timer is not None:
            self._timer.cancel()
        self._timer = self._timer_factory(max(0, remaining), self._expire)
        self._timer.daemon = True
        self._timer.start()

    def renew(self, request_started_at):
        expired = False
        with self._lock:
            if self._closed:
                return
            now = self._clock()
            deadline = request_started_at + TASK_LEASE_SECONDS
            if (self._deadline is not None and now >= self._deadline) or now >= deadline:
                self._closed = True
                if self._timer is not None:
                    self._timer.cancel()
                expired = True
            else:
                self._deadline = deadline
                self._arm_locked(deadline - now)
        if expired:
            self._on_expired()

    def _expire(self):
        with self._lock:
            if self._closed:
                return
            remaining = self._deadline - self._clock()
            if remaining > 0:
                self._arm_locked(remaining)
                return
            self._closed = True
        self._on_expired()

    def close(self):
        with self._lock:
            self._closed = True
            if self._timer is not None:
                self._timer.cancel()


class RetryableTaskError(Exception):
    """Opt in only when repeating a Task handler's external effects is safe."""


class TaskExecutionPolicy:
    def __init__(self, settings):
        for name, default in (
            ("max_attempts", 3), ("max_running", 16), ("max_running_per_owner", 4),
            ("retry_base_delay_ms", 1000), ("retry_max_delay_ms", 60000),
        ):
            value = settings.get(name)
            if value is None:
                value = default
            if isinstance(value, bool) or not isinstance(value, int):
                raise ValueError(f"tasks.{name} must be a positive integer")
            if value == 0:
                value = default
            if value < 1:
                raise ValueError(f"tasks.{name} must be a positive integer")
            setattr(self, name, value)
        if (self.max_attempts > 10 or self.max_running_per_owner > self.max_running
                or self.retry_base_delay_ms < 100
                or self.retry_max_delay_ms < self.retry_base_delay_ms
                or self.retry_max_delay_ms > 86400000):
            raise ValueError("MCP task execution limits or retry policy are outside their supported ranges")

    def retry_delay_ms(self, attempt):
        ceiling = min(self.retry_max_delay_ms, self.retry_base_delay_ms * 2 ** max(0, min(attempt - 1, 10)))
        return random.randint((ceiling + 1) // 2, ceiling)
