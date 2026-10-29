"""Explicit binding for new intents on an authenticated protected-job worker."""

from typing import Any

from faas_sdk.preview import EventAcknowledgmentIntentRetryPolicy


async def stage_completion_with_stored_policy(worker: Any, connection: Any, handler: Any, stop_event: Any) -> Any:
    """Upgrade storage and every recovery host before enabling this SQL-only handler path."""
    return await worker.run_durable(
        connection,
        handler,
        retry_policy=EventAcknowledgmentIntentRetryPolicy(initial_backoff_ms=1000, max_backoff_ms=300000),
        stop_event=stop_event,
    )
