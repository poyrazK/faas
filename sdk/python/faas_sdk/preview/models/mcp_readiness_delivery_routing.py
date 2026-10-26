from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_delivery_routing_failover_reason import (
    MCPReadinessDeliveryRoutingFailoverReason,
    check_mcp_readiness_delivery_routing_failover_reason,
)
from ..models.mcp_readiness_delivery_routing_last_destination import (
    MCPReadinessDeliveryRoutingLastDestination,
    check_mcp_readiness_delivery_routing_last_destination,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessDeliveryRouting")


@_attrs_define
class MCPReadinessDeliveryRouting:
    """Credential-free destination progress for one immutable readiness event. A recovery can remain pending after fallback
    succeeds while the previously informed primary is still outstanding.

    """

    policy_version: int
    """Fallback configuration version captured when this incident first claimed delivery."""
    primary_attempts: int
    """Number of primary dispatch claims including expired leases."""
    fallback_attempts: int
    """Number of fallback dispatch claims for this logical event."""
    primary_failures: int
    """Number of fenced unsuccessful primary completions for this event."""
    primary_delivered: bool
    """Whether primary accepted this event in a completed delivery claim."""
    fallback_delivered: bool
    """Whether fallback accepted this event in a completed delivery claim."""
    pagerduty_version: int | Unset = UNSET
    """Native primary policy version pinned at first delivery; omitted for legacy webhook routing."""
    last_destination: MCPReadinessDeliveryRoutingLastDestination | Unset = UNSET
    """Receiver role used for this event's most recent dispatch claim."""
    failover_reason: MCPReadinessDeliveryRoutingFailoverReason | Unset = UNSET
    """Explanation for selecting a receiver after failover; omitted for ordinary primary delivery."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_version = self.policy_version

        primary_attempts = self.primary_attempts

        fallback_attempts = self.fallback_attempts

        primary_failures = self.primary_failures

        primary_delivered = self.primary_delivered

        fallback_delivered = self.fallback_delivered

        pagerduty_version = self.pagerduty_version

        last_destination: str | Unset = UNSET
        if not isinstance(self.last_destination, Unset):
            last_destination = self.last_destination

        failover_reason: str | Unset = UNSET
        if not isinstance(self.failover_reason, Unset):
            failover_reason = self.failover_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_version": policy_version,
                "primary_attempts": primary_attempts,
                "fallback_attempts": fallback_attempts,
                "primary_failures": primary_failures,
                "primary_delivered": primary_delivered,
                "fallback_delivered": fallback_delivered,
            }
        )
        if pagerduty_version is not UNSET:
            field_dict["pagerduty_version"] = pagerduty_version
        if last_destination is not UNSET:
            field_dict["last_destination"] = last_destination
        if failover_reason is not UNSET:
            field_dict["failover_reason"] = failover_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        policy_version = d.pop("policy_version")

        primary_attempts = d.pop("primary_attempts")

        fallback_attempts = d.pop("fallback_attempts")

        primary_failures = d.pop("primary_failures")

        primary_delivered = d.pop("primary_delivered")

        fallback_delivered = d.pop("fallback_delivered")

        pagerduty_version = d.pop("pagerduty_version", UNSET)

        _last_destination = d.pop("last_destination", UNSET)
        last_destination: MCPReadinessDeliveryRoutingLastDestination | Unset
        if isinstance(_last_destination, Unset):
            last_destination = UNSET
        else:
            last_destination = check_mcp_readiness_delivery_routing_last_destination(_last_destination)

        _failover_reason = d.pop("failover_reason", UNSET)
        failover_reason: MCPReadinessDeliveryRoutingFailoverReason | Unset
        if isinstance(_failover_reason, Unset):
            failover_reason = UNSET
        else:
            failover_reason = check_mcp_readiness_delivery_routing_failover_reason(_failover_reason)

        mcp_readiness_delivery_routing = cls(
            policy_version=policy_version,
            primary_attempts=primary_attempts,
            fallback_attempts=fallback_attempts,
            primary_failures=primary_failures,
            primary_delivered=primary_delivered,
            fallback_delivered=fallback_delivered,
            pagerduty_version=pagerduty_version,
            last_destination=last_destination,
            failover_reason=failover_reason,
        )

        mcp_readiness_delivery_routing.additional_properties = d
        return mcp_readiness_delivery_routing

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
