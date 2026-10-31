from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.org_queue_usage_response_plan import OrgQueueUsageResponsePlan, check_org_queue_usage_response_plan

T = TypeVar("T", bound="OrgQueueUsageResponse")


@_attrs_define
class OrgQueueUsageResponse:
    """Company queue counters and current plan limits across retained application namespaces."""

    organization_id: UUID
    plan: OrgQueueUsageResponsePlan
    bindings: int
    pending: int
    processing: int
    dead_letter: int
    reserved_async: int
    """Reservations across all async invocation sources sharing company capacity."""
    max_async_invocations: int
    max_triggers: int

    def to_dict(self) -> dict[str, Any]:
        organization_id = str(self.organization_id)

        plan: str = self.plan

        bindings = self.bindings

        pending = self.pending

        processing = self.processing

        dead_letter = self.dead_letter

        reserved_async = self.reserved_async

        max_async_invocations = self.max_async_invocations

        max_triggers = self.max_triggers

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "organization_id": organization_id,
                "plan": plan,
                "bindings": bindings,
                "pending": pending,
                "processing": processing,
                "dead_letter": dead_letter,
                "reserved_async": reserved_async,
                "max_async_invocations": max_async_invocations,
                "max_triggers": max_triggers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        organization_id = UUID(d.pop("organization_id"))

        plan = check_org_queue_usage_response_plan(d.pop("plan"))

        bindings = d.pop("bindings")

        pending = d.pop("pending")

        processing = d.pop("processing")

        dead_letter = d.pop("dead_letter")

        reserved_async = d.pop("reserved_async")

        max_async_invocations = d.pop("max_async_invocations")

        max_triggers = d.pop("max_triggers")

        org_queue_usage_response = cls(
            organization_id=organization_id,
            plan=plan,
            bindings=bindings,
            pending=pending,
            processing=processing,
            dead_letter=dead_letter,
            reserved_async=reserved_async,
            max_async_invocations=max_async_invocations,
            max_triggers=max_triggers,
        )

        return org_queue_usage_response
