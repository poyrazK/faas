"""Sequential reviewed execution recovery with fresh acceptance-guarded reads."""

from __future__ import annotations

import asyncio
import copy
import hashlib
from typing import Any
from uuid import UUID

from .client import AuthenticatedClient
from .commit_delivery_report import aget_commit_delivery_report
from .commit_recovery_plan import aplan_commit_recovery


class CommitRecoveryApplyError(Exception):
    def __init__(self, result: dict[str, Any]):
        self.result = result
        super().__init__("Commit recovery apply stopped; inspect per-item results before retrying")


async def aapply_commit_recovery(
    client: AuthenticatedClient,
    source: UUID | str,
    reviewed: dict[str, Any],
    *,
    limit: int = 50,
    recipient_pages: int = 1,
    timeout: float = 120,
) -> dict[str, Any]:
    """Apply known execution recovery only. Never automatically retry ambiguous writes."""
    source = str(UUID(str(source)))
    plan = copy.deepcopy(reviewed)
    out: dict[str, Any] = {"source_id": source, "items": [], "counts": {}, "complete": False}
    if (
        not isinstance(limit, int)
        or not 1 <= limit <= 50
        or not isinstance(recipient_pages, int)
        or not 1 <= recipient_pages <= 20
        or not 0 < timeout <= 86400
        or plan["report"]["source_id"] != source
        or len(plan["items"]) > 200 * 200 * 20
        or len(plan["report"]["items"]) > 200
    ):
        raise ValueError("invalid apply budgets or plan")
    publications = {item["acceptance"]["event_id"].lower(): item for item in plan["report"]["items"]}
    seen: set[str] = set()
    for item in plan["items"]:
        sub = str(UUID(item["subscription_id"]))
        key = f"{item['event_id']}:{sub}".lower()
        if (
            item["event_id"].lower() not in publications
            or key in seen
            or (plan["report"].get("subscription_id") and plan["report"]["subscription_id"].lower() != sub)
            or item["status"] not in {"eligible", "skipped"}
            or (item["status"] == "eligible" and not item.get("action"))
        ):
            raise ValueError("invalid reviewed selection")
        seen.add(key)
    used, stopped = 0, False
    cause: BaseException | None = None
    try:
        async with asyncio.timeout(timeout):
            await aget_commit_delivery_report(
                client, source, refresh=plan["report"], recipient_pages=1, timeout=timeout
            )
            for item in plan["items"]:
                result: dict[str, Any] = {
                    "event_id": item["event_id"],
                    "subscription_id": item["subscription_id"],
                    "status": "skipped",
                    "reason": "not_reviewed_eligible",
                }
                if stopped:
                    result.update(status="not_attempted", reason="batch_stopped")
                elif item["status"] == "eligible":
                    if used >= limit:
                        result.update(status="not_attempted", reason="apply_budget")
                    else:
                        used += 1
                        original = copy.deepcopy(publications[item["event_id"].lower()])
                        original["subscribers"] = []
                        saved = {
                            **plan["report"],
                            "subscription_id": item["subscription_id"].lower(),
                            "items": [original],
                        }
                        current = None
                        try:
                            current = await aplan_commit_recovery(
                                client, source, saved, recipient_pages=recipient_pages, timeout=timeout
                            )
                        except Exception:
                            result.update(status="read_error", reason="eligibility_read_failed")
                        if current is not None:
                            fresh = current["items"][0] if current["items"] else None
                            if fresh is None:
                                result["reason"] = (
                                    current["report"]["items"][0]["evidence_status"]
                                    if current["report"]["items"]
                                    else "evidence_unavailable"
                                )
                            elif (
                                item.get("action", {}).get("kind") == "routing_replay"
                                and type(item.get("action", {}).get("body", {}).get("expected_generation")) is not int
                            ):
                                result["reason"] = "generation_guard_unavailable"
                            elif (
                                item.get("action", {}).get("kind") == "routing_replay"
                                and current["report"]["items"][0]["subscribers"][0]
                                .get("recipient", {})
                                .get("routing", {})
                                .get("generation")
                                is None
                            ):
                                result["reason"] = "generation_guard_unavailable"
                            elif (
                                item.get("action", {}).get("kind") == "routing_replay"
                                and item["action"]["body"]["expected_generation"]
                                != current["report"]["items"][0]["subscribers"][0]["recipient"]["routing"]["generation"]
                            ):
                                result["reason"] = "generation_changed"
                            elif fresh["status"] != "eligible":
                                result["reason"] = fresh.get("reason", "no_recovery_action")
                            elif item.get("action") != fresh.get("action"):
                                result["reason"] = "action_changed"
                            elif fresh["action"]["kind"] == "routing_replay" and not fresh["action"].get(
                                "body", {}
                            ).get("expected_accepted_at"):
                                result["reason"] = "acceptance_guard_unavailable"
                            else:
                                try:
                                    routing = fresh["action"]["kind"] == "routing_replay"
                                    publication = original["acceptance"]["publication"]
                                    identity = fresh["action"]["kind"] + "\n" + fresh["action"]["url"]
                                    if routing:
                                        identity += (
                                            "\n"
                                            + publication["source"]
                                            + "\n"
                                            + publication["id"]
                                            + "\n"
                                            + item["subscription_id"].lower()
                                            + "\n"
                                            + fresh["action"]["body"]["expected_accepted_at"]
                                            + "\n"
                                            + str(fresh["action"]["body"]["expected_generation"])
                                        )
                                    key = "commit-recovery-" + hashlib.sha256(identity.encode()).hexdigest()
                                    response = await client.get_async_httpx_client().post(
                                        fresh["action"]["url"],
                                        json=fresh["action"]["body"] if routing else None,
                                        headers={"Idempotency-Key": key},
                                        follow_redirects=False,
                                    )
                                    status = response.status_code
                                    if 200 <= status < 300:
                                        receipt = response.json()
                                        if routing:
                                            if (
                                                not isinstance(receipt, dict)
                                                or receipt.get("event_source") != publication["source"]
                                                or receipt.get("event_id") != publication["id"]
                                                or str(receipt.get("subscription_id", "")).lower()
                                                != item["subscription_id"].lower()
                                                or receipt.get("state") != "pending"
                                            ):
                                                raise RuntimeError("routing recovery acknowledgement unavailable")
                                        elif (
                                            not isinstance(receipt, dict)
                                            or not isinstance(receipt.get("id"), str)
                                            or not receipt["id"]
                                        ):
                                            raise RuntimeError("recovery acknowledgement unavailable")
                                        result.update(status="queued", result=receipt)
                                        result.pop("reason", None)
                                    elif 400 <= status < 500 and status != 408:
                                        result.update(status="rejected", reason=f"http_{status}")
                                        if status == 409:
                                            try:
                                                problem = response.json()
                                            except ValueError:
                                                problem = None
                                            if (
                                                isinstance(problem, dict)
                                                and problem.get("code") == "event_replay_generation_changed"
                                            ):
                                                result.update(status="skipped", reason="generation_changed")
                                    else:
                                        raise RuntimeError(f"ambiguous recovery response: HTTP {status}")
                                except (Exception, asyncio.CancelledError) as error:
                                    result.update(status="unknown", reason="inspect_before_retry")
                                    stopped, cause = True, error
                out["items"].append(result)
                status = result["status"]
                out["counts"][status] = out["counts"].get(status, 0) + 1
            out["complete"] = not stopped
            if stopped:
                raise CommitRecoveryApplyError(out) from cause
            return out
    except CommitRecoveryApplyError:
        raise
    except (Exception, asyncio.CancelledError) as error:
        for item in plan["items"][len(out["items"]) :]:
            out["items"].append(
                {
                    "event_id": item["event_id"],
                    "subscription_id": item["subscription_id"],
                    "status": "not_attempted",
                    "reason": "batch_stopped",
                }
            )
            out["counts"]["not_attempted"] = out["counts"].get("not_attempted", 0) + 1
        raise CommitRecoveryApplyError(out) from error
