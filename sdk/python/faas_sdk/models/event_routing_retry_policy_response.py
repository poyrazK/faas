from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_routing_retry_policy import EventRoutingRetryPolicy


T = TypeVar("T", bound="EventRoutingRetryPolicyResponse")


@_attrs_define
class EventRoutingRetryPolicyResponse:
    """Configured routing retry policy for an application event subscription, or its default policy."""

    subscription_id: UUID
    configured: bool
    policy: EventRoutingRetryPolicy
    """Routing policy before invocation admission. Duration budgets include routing attempt time and scheduled
    retry delays. Admission waits add no budget cost. API replacement accepts explicit settings; manifests and CLI
    default configured policies to jitter enabled."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        configured = self.configured

        policy = self.policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "configured": configured,
                "policy": policy,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_routing_retry_policy import EventRoutingRetryPolicy

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        configured = d.pop("configured")

        policy = EventRoutingRetryPolicy.from_dict(d.pop("policy"))

        event_routing_retry_policy_response = cls(
            subscription_id=subscription_id,
            configured=configured,
            policy=policy,
        )

        return event_routing_retry_policy_response
