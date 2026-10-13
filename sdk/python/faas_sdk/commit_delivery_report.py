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


class CommitDeliveryReadError(Exception):
    def __init__(self, status_code: int, headers: Any):
        self.status_code = status_code
        self.retry_after = headers.get("Retry-After")
        super().__init__(f"delivery evidence read failed: HTTP {status_code}")


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
    refresh: dict[str, Any] | None = None,
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
    if resume is not None and refresh is not None:
        raise ValueError("choose resume or refresh")
    saved = resume if resume is not None else refresh
    report: dict[str, Any] = (
        copy.deepcopy(saved)
        if saved is not None
        else {"source_id": source, "items": [], "counts": {}, "read_complete": False}
    )
    if saved is not None and (
        before is not None
        or subscription is not None
        or limit != 50
        or report.get("source_id") != source
        or len(report.get("items", [])) > 200
    ):
        raise ValueError("resume fixes publication selection and subscriber filter")
    subscription = report.get("subscription_id") if saved is not None else subscription
    if subscription:
        subscription = str(UUID(subscription))
        if UUID(subscription).int == 0:
            raise ValueError("invalid saved subscriber filter")
    if resume is not None:
        report.pop("changes", None)
    baseline = copy.deepcopy(report) if refresh is not None else None
    if refresh is not None:
        report["changes"] = []
        for item in report["items"]:
            item["subscribers"] = []
            item["evidence_status"] = "not_read"
            item.pop("next_after", None)
            item.pop("observed_at", None)
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
            if saved is not None:
                binding = await get_commit_source.asyncio_detailed(client=client, source=UUID(source))
                if binding.status_code != 200:
                    raise CommitDeliveryReadError(binding.status_code, binding.headers)
                if not hasattr(binding.parsed, "app_id") or binding.parsed.destination != "event":
                    raise ValueError("saved report requires an accessible event source")
                ids: set[str] = set()
                for item in (baseline if baseline is not None else report)["items"]:
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
                    raise CommitDeliveryReadError(history.status_code, history.headers)
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
                        current["observed_at"] = datetime.datetime.now(datetime.UTC).isoformat()
                        current["evidence_status"] = (
                            "unavailable" if response.status_code == 404 else "acceptance_replaced"
                        )
                        break
                    if response.status_code != 200 or not isinstance(response.parsed, EventReceiptResponse):
                        raise CommitDeliveryReadError(response.status_code, response.headers)
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
                                "recovery": _recovery(row),
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
                    current["observed_at"] = datetime.datetime.now(datetime.UTC).isoformat()
                    current["next_after"] = page.get("next_after", "")
                    if subscription and not current["subscribers"] and current["evidence_status"] == "available":
                        current["subscribers"].append(
                            {"publication": publication, "subscription_id": subscription, "status": "not_targeted"}
                        )

                        report["counts"]["not_targeted"] = report["counts"].get("not_targeted", 0) + 1
                    if not current.get("next_after"):
                        break
            if baseline is not None:
                report["changes"] = _compare_reports(baseline, report)
            report["read_complete"] = True
            return report
    except Exception as error:
        if current is not None:
            current["evidence_status"] = "read_error"
        if baseline is not None:
            report["changes"] = _compare_reports(baseline, report)
        raise CommitDeliveryReportError(report) from error
    finally:
        report["recovery_counts"] = {}
        for item in report["items"]:
            for row in item["subscribers"]:
                if row.get("recovery"):
                    status = row["recovery"]["status"]
                    report["recovery_counts"][status] = report["recovery_counts"].get(status, 0) + 1


def _recovery(row: dict[str, Any]) -> dict[str, Any]:
    result: dict[str, Any] = {"status": "unavailable"}
    evidence = row.get("recovery")
    if row.get("workflow_name") or not evidence:
        return result
    result["history_url"] = evidence.get("history_url", "")
    result["retained_replay_count"] = evidence.get("retained_replay_count", 0)
    latest = evidence.get("latest_replay")
    if latest:
        result["invocation_id"] = latest.get("invocation_id", "")
        result["state"] = latest.get("state", "")
        state = result["state"]
        if state == "completed":
            result["status"] = "recovered"
        elif state in {"pending", "dispatching"}:
            result["status"] = "pending"
        elif state in {"failed", "dead_letter", "cancelled", "expired", "superseded"}:
            result["status"] = "failed"
    return result


def _compare_reports(previous: dict[str, Any], current: dict[str, Any]) -> list[dict[str, Any]]:
    changes: list[dict[str, Any]] = []
    for old, item in zip(previous["items"], current["items"], strict=True):
        if not item.get("observed_at"):
            continue
        common = {"event_id": item["acceptance"]["event_id"], "observed_at": item["observed_at"]}
        if old.get("observed_at"):
            common["previous_observed_at"] = old["observed_at"]
        if old["evidence_status"] != item["evidence_status"] and item["evidence_status"] not in {
            "not_read",
            "read_error",
        }:
            changes.append(
                {
                    **common,
                    "kind": "evidence",
                    "previous_status": old["evidence_status"],
                    "status": item["evidence_status"],
                }
            )
        recoveries = {
            row["subscription_id"].lower(): row.get("recovery", {}).get("status", "unobserved")
            for row in old["subscribers"]
        }
        statuses = {row["subscription_id"].lower(): row["status"] for row in old["subscribers"]}
        for row in item["subscribers"]:
            before = statuses.get(row["subscription_id"].lower(), "unobserved")
            prior = recoveries.get(row["subscription_id"].lower(), "unobserved")
            if row.get("recovery") and prior != row["recovery"]["status"]:
                changes.append(
                    {
                        **common,
                        "kind": "recovery",
                        "subscription_id": row["subscription_id"],
                        "previous_status": prior,
                        "status": row["recovery"]["status"],
                    }
                )
            if before != row["status"]:
                changes.append(
                    {
                        **common,
                        "kind": "subscriber",
                        "subscription_id": row["subscription_id"],
                        "previous_status": before,
                        "status": row["status"],
                    }
                )
    return changes
