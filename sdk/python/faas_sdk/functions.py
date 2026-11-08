"""Config declared next to a function's code (ADR-20261107193000).

``gregale deploy`` reads the arguments of ``@function(...)`` on ``handler``
(or a module-level ``config = {...}`` dict) statically; it never imports the
module. Keep every argument a literal: strings, numbers, booleans, None,
lists and dicts. Put values that change between deploys in env or secrets.
Fields that gregale.yaml also sets take its value.

    from faas_sdk import function

    @function(cron="0 8 * * *", ram_mb=512)
    def handler(event, ctx):
        return {"status": 200, "body": {"ok": True}}
"""

from __future__ import annotations

from typing import Any, Callable, TypeVar

F = TypeVar("F", bound=Callable[..., Any])

# The keys a function may declare: the fields of a `functions:` entry except
# name, root and handler, plus the `cron` and `queue` shorthands.
FUNCTION_CONFIG_KEYS = frozenset(
    {
        "cron",
        "queue",
        "runtime",
        "ram_mb",
        "request_timeout_s",
        "idle_timeout_s",
        "min_instances",
        "concurrency",
        "env",
        "prune_env",
        "secrets_from_env",
        "routes",
        "entry",
        "bucket_events",
        "webhooks",
        "queue_bindings",
        "event_triggers",
    }
)


def function(**config: Any) -> Callable[[F], F]:
    """Declare a function's deploy settings; returns the handler unchanged.

    The settings are also stored on the handler as ``__gregale_config__``.
    An unknown key raises ``TypeError`` at import, as ``gregale deploy``
    would refuse it.
    """
    unknown = sorted(set(config) - FUNCTION_CONFIG_KEYS)
    if unknown:
        raise TypeError(f"unknown function config key(s): {', '.join(unknown)}")

    def decorate(handler: F) -> F:
        handler.__gregale_config__ = dict(config)  # type: ignore[attr-defined]
        return handler

    return decorate


__all__ = ("FUNCTION_CONFIG_KEYS", "function")
