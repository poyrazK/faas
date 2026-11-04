"""Execution limits and retry policy shared by web, workers and observers."""

import random


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
