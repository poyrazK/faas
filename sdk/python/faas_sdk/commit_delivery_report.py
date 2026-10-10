"""Bounded, read-only delivery reports; counts cover sampled original recipients."""

from __future__ import annotations

import asyncio
import math
from typing import Any
from uuid import UUID

from .api.events import get_event_receipt
from .api.queues import list_commit_publications
from .client import AuthenticatedClient
from .commit_subscriber_wait import _status
from .models.commit_publication_history_response import CommitPublicationHistoryResponse
from .models.event_receipt_response import EventReceiptResponse


class CommitDeliveryReportError(Exception):
    """A read failed; report retains partial observations and unread items."""

    def __init__(self, report: dict[str, Any]):
        self.report = report
        super().__init__("Commit delivery report read failed; partial observations are attached")


async def aget_commit_delivery_report(
    client: AuthenticatedClient,
    source: UUID | str,
    *,
    before: str | None = None,
    limit: int = 50,
    subscription_id: UUID | str | None = None,
    timeout: float = 120,
) -> dict[str, Any]:
    """Read one history page and one guarded recipient page per publication.

    read_complete describes reads, not consumer completion. On read failure or
    timeout, CommitDeliveryReportError.report retains partial evidence. Task
    cancellation propagates and does not cancel any durable work.
    """
    source = str(UUID(str(source)))
    subscription = str(UUID(str(subscription_id))) if subscription_id is not None else None
    if (
        UUID(source).int == 0
        or (subscription and UUID(subscription).int == 0)
        or not isinstance(limit, int)
        or not 1 <= limit <= 200
        or not math.isfinite(timeout)
        or not 0 < timeout <= 86400
    ):
        raise ValueError("invalid delivery report options")
    report: dict[str, Any] = {"source_id": source, "items": [], "counts": {}, "read_complete": False}
    if subscription:
        report["subscription_id"] = subscription
    current: dict[str, Any] | None = None
    try:
        async with asyncio.timeout(timeout):
            kwargs: dict[str, Any] = {"source": UUID(source), "limit": limit}
            if before is not None:
                kwargs["before"] = before
            history = await list_commit_publications.asyncio_detailed(client=client, **kwargs)
            if history.status_code != 200 or not isinstance(history.parsed, CommitPublicationHistoryResponse):
                raise RuntimeError(f"read publication history failed: HTTP {history.status_code}")
            data = history.parsed.to_dict()
            if data.get("next_before"):
                report["next_before"] = data["next_before"]
            report["items"] = [
                {"acceptance": item, "evidence_status": "not_read", "subscribers": []} for item in data["items"]
            ]
            for current, model in zip(report["items"], history.parsed.items, strict=True):
                publication = current["acceptance"].get("publication")
                if not publication:
                    raise RuntimeError("publication history lacks publication identity")
                response = await get_event_receipt.asyncio_detailed(
                    client=client,
                    source=publication["source"],
                    id=publication["id"],
                    expected_accepted_at=model.publication.accepted_at,
                    limit=200,
                )
                if response.status_code in {404, 409}:
                    current["evidence_status"] = "unavailable" if response.status_code == 404 else "acceptance_replaced"
                    continue
                if response.status_code != 200 or not isinstance(response.parsed, EventReceiptResponse):
                    raise RuntimeError(f"read subscriber evidence failed: HTTP {response.status_code}")
                page = response.parsed.to_dict()
                if page["event_source"] != publication["source"] or page["event_id"] != publication["id"]:
                    raise RuntimeError("publication evidence identity mismatch")
                current["evidence_status"] = (
                    "partial" if page.get("next_after") or not page["snapshot_captured"] else "available"
                )
                if page.get("next_after"):
                    current["next_after"] = page["next_after"]
                for row in page["recipients"]:
                    if row.get("origin", "acceptance") != "acceptance" or (
                        subscription and row["subscription_id"].lower() != subscription
                    ):
                        continue
                    status = _status(row)
                    current["subscribers"].append(
                        {
                            "publication": publication,
                            "subscription_id": row["subscription_id"],
                            "status": status,
                            "recipient": row,
                        }
                    )
                    report["counts"][status] = report["counts"].get(status, 0) + 1
                if subscription and not current["subscribers"] and current["evidence_status"] == "available":
                    current["subscribers"].append(
                        {"publication": publication, "subscription_id": subscription, "status": "not_targeted"}
                    )
                    report["counts"]["not_targeted"] = report["counts"].get("not_targeted", 0) + 1
            report["read_complete"] = True
            return report
    except Exception as error:
        if current is not None:
            current["evidence_status"] = "read_error"
        raise CommitDeliveryReportError(report) from error
