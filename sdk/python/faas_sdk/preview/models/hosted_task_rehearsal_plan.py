from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.hosted_task_rehearsal_plan_status import (
    HostedTaskRehearsalPlanStatus,
    check_hosted_task_rehearsal_plan_status,
)
from ..models.hosted_task_rehearsal_plan_version import (
    HostedTaskRehearsalPlanVersion,
    check_hosted_task_rehearsal_plan_version,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="HostedTaskRehearsalPlan")


@_attrs_define
class HostedTaskRehearsalPlan:
    """Owner-only plan metadata. A target identity reserves future allocation and creates no resources."""

    version: HostedTaskRehearsalPlanVersion
    account_id: UUID
    intent_id: str
    intent_sha256: str
    review_sha256: str
    binding_sha256: str
    target_id: UUID
    queue_namespace: str
    plan_sha256: str
    status: HostedTaskRehearsalPlanStatus
    created_at: datetime.datetime
    binding_checked_at: datetime.datetime
    expires_at: datetime.datetime
    no_traffic: bool
    approved_at: datetime.datetime | Unset = UNSET
    claimed_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        account_id = str(self.account_id)

        intent_id = self.intent_id

        intent_sha256 = self.intent_sha256

        review_sha256 = self.review_sha256

        binding_sha256 = self.binding_sha256

        target_id = str(self.target_id)

        queue_namespace = self.queue_namespace

        plan_sha256 = self.plan_sha256

        status: str = self.status

        created_at = self.created_at.isoformat()

        binding_checked_at = self.binding_checked_at.isoformat()

        expires_at = self.expires_at.isoformat()

        no_traffic = self.no_traffic

        approved_at: str | Unset = UNSET
        if not isinstance(self.approved_at, Unset):
            approved_at = self.approved_at.isoformat()

        claimed_at: str | Unset = UNSET
        if not isinstance(self.claimed_at, Unset):
            claimed_at = self.claimed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "account_id": account_id,
                "intent_id": intent_id,
                "intent_sha256": intent_sha256,
                "review_sha256": review_sha256,
                "binding_sha256": binding_sha256,
                "target_id": target_id,
                "queue_namespace": queue_namespace,
                "plan_sha256": plan_sha256,
                "status": status,
                "created_at": created_at,
                "binding_checked_at": binding_checked_at,
                "expires_at": expires_at,
                "no_traffic": no_traffic,
            }
        )
        if approved_at is not UNSET:
            field_dict["approved_at"] = approved_at
        if claimed_at is not UNSET:
            field_dict["claimed_at"] = claimed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_hosted_task_rehearsal_plan_version(d.pop("version"))

        account_id = UUID(d.pop("account_id"))

        intent_id = d.pop("intent_id")

        intent_sha256 = d.pop("intent_sha256")

        review_sha256 = d.pop("review_sha256")

        binding_sha256 = d.pop("binding_sha256")

        target_id = UUID(d.pop("target_id"))

        queue_namespace = d.pop("queue_namespace")

        plan_sha256 = d.pop("plan_sha256")

        status = check_hosted_task_rehearsal_plan_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        binding_checked_at = datetime.datetime.fromisoformat(d.pop("binding_checked_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        no_traffic = d.pop("no_traffic")

        _approved_at = d.pop("approved_at", UNSET)
        approved_at: datetime.datetime | Unset
        if isinstance(_approved_at, Unset):
            approved_at = UNSET
        else:
            approved_at = datetime.datetime.fromisoformat(_approved_at)

        _claimed_at = d.pop("claimed_at", UNSET)
        claimed_at: datetime.datetime | Unset
        if isinstance(_claimed_at, Unset):
            claimed_at = UNSET
        else:
            claimed_at = datetime.datetime.fromisoformat(_claimed_at)

        hosted_task_rehearsal_plan = cls(
            version=version,
            account_id=account_id,
            intent_id=intent_id,
            intent_sha256=intent_sha256,
            review_sha256=review_sha256,
            binding_sha256=binding_sha256,
            target_id=target_id,
            queue_namespace=queue_namespace,
            plan_sha256=plan_sha256,
            status=status,
            created_at=created_at,
            binding_checked_at=binding_checked_at,
            expires_at=expires_at,
            no_traffic=no_traffic,
            approved_at=approved_at,
            claimed_at=claimed_at,
        )

        return hosted_task_rehearsal_plan
