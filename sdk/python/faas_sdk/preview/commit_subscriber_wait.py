"""Read-only waits for the original acceptance subscriber, excluding recovery/backfill."""

from __future__ import annotations

import asyncio
import math
from typing import Any
from uuid import UUID

from .api.events import get_event_receipt
from .client import AuthenticatedClient
from .models.commit_event_publication import CommitEventPublication
from .models.event_receipt_response import EventReceiptResponse


def _status(row: dict[str, Any]) -> str:
    if row.get("workflow_name"):
        return "unsupported_recipient"
    execution = row.get("execution")
    if execution:
        state = execution["state"]
        if state in {"completed", "failed", "dead_letter", "cancelled", "expired", "superseded"}:
            return str(state)
        return "pending" if state in {"pending", "dispatching"} else "unavailable"
    if row.get("cancellation"):
        return "cancelled"
    if row.get("execution_unavailable"):
        return "unavailable"
    state = row["routing"]["state"]
    if state == "filtered":
        return "filtered"
    if state == "failed":
        return "routing_failed"
    return "pending" if state in {"pending", "processing"} else "unavailable"


async def await_commit_subscriber(
    client: AuthenticatedClient,
    publication: CommitEventPublication,
    subscription_id: UUID | str,
    *,
    timeout: float = 120.0,
    interval: float = 1.0,
) -> dict[str, Any]:
    """Return original subscriber outcome. Cancellation propagates; never cancels work.

    Timeout returns timed_out with the last observation. Transport failures propagate.
    The original datetime precision is preserved in the acceptance-time guard.
    """
    subscription = str(UUID(str(subscription_id)))
    if (
        UUID(subscription).int == 0
        or not math.isfinite(timeout)
        or not 0 < timeout <= 86400
        or not math.isfinite(interval)
        or not 0.1 <= interval <= 60
    ):
        raise ValueError("invalid subscriber wait options")
    # Snapshot caller-owned model before any await.
    saved = publication.to_dict()
    source, event_id, accepted_at = saved["source"], saved["id"], publication.accepted_at
    if not source or not event_id or not accepted_at:
        raise ValueError("invalid publication identity")
    result: dict[str, Any] = {"publication": saved, "subscription_id": subscription, "status": "pending"}
    try:
        async with asyncio.timeout(timeout):
            while True:
                after: str | None = None
                seen: set[str] = set()
                found = False
                while True:
                    kwargs: dict[str, Any] = {
                        "source": source,
                        "id": event_id,
                        "expected_accepted_at": accepted_at,
                        "limit": 200,
                    }
                    if after is not None:
                        kwargs["after"] = after
                    response = await get_event_receipt.asyncio_detailed(client=client, **kwargs)
                    if response.status_code in {404, 409}:
                        result["status"] = "unavailable" if response.status_code == 404 else "acceptance_replaced"
                        return result
                    if response.status_code != 200 or not isinstance(response.parsed, EventReceiptResponse):
                        raise RuntimeError(f"read subscriber evidence failed: HTTP {response.status_code}")
                    page = response.parsed.to_dict()
                    if page["event_source"] != source or page["event_id"] != event_id:
                        raise RuntimeError("publication evidence identity mismatch")
                    for row in page["recipients"]:
                        if (
                            row["subscription_id"].lower() == subscription
                            and row.get("origin", "acceptance") == "acceptance"
                        ):
                            result["recipient"], result["status"] = row, _status(row)
                            found = True
                            break
                    if found:
                        break
                    after = page.get("next_after")
                    if not after:
                        if page["snapshot_captured"] and page.get("routing_settled_at"):
                            result["status"] = "not_targeted"
                            return result
                        break
                    if after in seen:
                        raise RuntimeError("repeated publication recipient cursor")
                    seen.add(after)
                if found and result["status"] != "pending":
                    return result
                await asyncio.sleep(interval)
    except TimeoutError:
        result["status"] = "timed_out"
        return result
