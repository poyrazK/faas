"""Bounded, read-only delivery reports; counts cover sampled original recipients."""

from __future__ import annotations

import asyncio
import copy
import datetime
import math
from typing import Any
from uuid import UUID

from .api.events import get_event_receipt
from .api.queues import get_commit_source, list_commit_publications
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
    recipient_pages: int = 1,
    resume: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Read one history page and one guarded recipient page per publication.

    read_complete describes reads, not consumer completion. On read failure or
    timeout, CommitDeliveryReportError.report retains partial evidence. Task
    cancellation propagates and does not cancel any durable work.
    """
    source = str(UUID(str(source)))
    subscription = str(UUID(str(subscription_id))) if subscription_id is not None else None
    if (
        not isinstance(recipient_pages, int)
        or not 1 <= recipient_pages <= 20
        or UUID(source).int == 0
        or (subscription and UUID(subscription).int == 0)
        or not isinstance(limit, int)
        or not 1 <= limit <= 200
        or not math.isfinite(timeout)
        or not 0 < timeout <= 86400
    ):
        raise ValueError("invalid delivery report options")
    report: dict[str, Any] = (
        copy.deepcopy(resume)
        if resume is not None
        else {"source_id": source, "items": [], "counts": {}, "read_complete": False}
    )
    if resume is not None and (
        before is not None
        or subscription is not None
        or limit != 50
        or report.get("source_id") != source
        or len(report.get("items", [])) > 200
    ):
        raise ValueError("resume fixes publication selection and subscriber filter")
    subscription = report.get("subscription_id") if resume is not None else subscription
    if subscription:
        subscription = str(UUID(subscription))
        if UUID(subscription).int == 0:
            raise ValueError("invalid saved subscriber filter")
    report["read_complete"] = False

    def recount() -> None:
        report["counts"] = {}
        for item in report["items"]:
            for row in item["subscribers"]:
                status = row["status"]
                report["counts"][status] = report["counts"].get(status, 0) + 1

    recount()
    if subscription:
        report["subscription_id"] = subscription
    current: dict[str, Any] | None = None
    try:
        async with asyncio.timeout(timeout):
            if resume is not None:
                binding = await get_commit_source.asyncio_detailed(client=client, source=UUID(source))
                if (
                    binding.status_code != 200
                    or not hasattr(binding.parsed, "app_id")
                    or binding.parsed.destination != "event"
                ):
                    raise ValueError("saved report requires an accessible event source")
                ids: set[str] = set()
                for item in report["items"]:
                    a, publication = item["acceptance"], item["acceptance"].get("publication")
                    if (
                        not publication
                        or str(UUID(a["source_id"])) != source
                        or str(UUID(a["event_id"])) in ids
                        or publication["source"] != f"app.{binding.parsed.app_id}"
                        or publication["id"] != f"commit.{source}.{UUID(a['event_id'])}"
                        or not publication["accepted_at"]
                        or item["evidence_status"]
                        not in {"available", "partial", "unavailable", "acceptance_replaced", "not_read", "read_error"}
                    ):
                        raise ValueError("saved publication identity mismatch")
                    ids.add(str(UUID(a["event_id"])))
                    subs: set[str] = set()
                    for row in item["subscribers"]:
                        sub = str(UUID(row["subscription_id"]))
                        if sub in subs or (subscription and sub != subscription):
                            raise ValueError("invalid saved subscriber identity")
                        subs.add(sub)
            else:
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
            for current in report["items"]:
                if resume is not None and current["evidence_status"] in {
                    "available",
                    "unavailable",
                    "acceptance_replaced",
                }:
                    continue
                seen: set[str] = set()
                for _ in range(recipient_pages):
                    publication = current["acceptance"].get("publication")
                    if not publication:
                        raise RuntimeError("publication history lacks publication identity")
                    after = current.get("next_after", "")
                    if after in seen:
                        raise RuntimeError("repeated publication recipient cursor")
                    seen.add(after)
                    response = await get_event_receipt.asyncio_detailed(
                        client=client,
                        source=publication["source"],
                        id=publication["id"],
                        expected_accepted_at=datetime.datetime.fromisoformat(
                            publication["accepted_at"].replace("Z", "+00:00")
                        ),
                        after=current.get("next_after", ""),
                        limit=200,
                    )
                    if response.status_code in {404, 409}:
                        current["evidence_status"] = (
                            "unavailable" if response.status_code == 404 else "acceptance_replaced"
                        )
                        break
                    if response.status_code != 200 or not isinstance(response.parsed, EventReceiptResponse):
                        raise RuntimeError(f"read subscriber evidence failed: HTTP {response.status_code}")
                    page = response.parsed.to_dict()
                    if (
                        page["event_source"] != publication["source"]
                        or page["event_id"] != publication["id"]
                        or (page.get("next_after") and (page["next_after"] == after or page["next_after"] in seen))
                    ):
                        raise RuntimeError("publication evidence identity mismatch")
                    current["evidence_status"] = (
                        "partial" if page.get("next_after") or not page["snapshot_captured"] else "available"
                    )
                    additions: list[dict[str, Any]] = []
                    for row in page["recipients"]:
                        if row.get("origin", "acceptance") != "acceptance" or (
                            subscription and row["subscription_id"].lower() != subscription
                        ):
                            continue
                        status = _status(row)
                        additions.append(
                            {
                                "publication": publication,
                                "subscription_id": row["subscription_id"],
                                "status": status,
                                "recipient": row,
                            }
                        )

                    for row in additions:
                        index = next(
                            (
                                i
                                for i, saved in enumerate(current["subscribers"])
                                if saved["subscription_id"].lower() == row["subscription_id"].lower()
                            ),
                            None,
                        )
                        if index is None:
                            current["subscribers"].append(row)
                        else:
                            old = current["subscribers"][index]["status"]
                            report["counts"][old] -= 1
                            if report["counts"][old] == 0:
                                del report["counts"][old]
                            current["subscribers"][index] = row
                        report["counts"][row["status"]] = report["counts"].get(row["status"], 0) + 1
                    current["next_after"] = page.get("next_after", "")
                    if subscription and not current["subscribers"] and current["evidence_status"] == "available":
                        current["subscribers"].append(
                            {"publication": publication, "subscription_id": subscription, "status": "not_targeted"}
                        )

                        report["counts"]["not_targeted"] = report["counts"].get("not_targeted", 0) + 1
                    if not current.get("next_after"):
                        break
            report["read_complete"] = True
            return report
    except Exception as error:
        if current is not None:
            current["evidence_status"] = "read_error"
        raise CommitDeliveryReportError(report) from error
