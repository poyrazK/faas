from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_notification_queue_failure_kind import (
    MCPReadinessNotificationQueueFailureKind,
    check_mcp_readiness_notification_queue_failure_kind,
)
from ..models.mcp_readiness_notification_queue_state import (
    MCPReadinessNotificationQueueState,
    check_mcp_readiness_notification_queue_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_destination_queue import MCPReadinessDestinationQueue
    from ..models.mcp_readiness_incident_event import MCPReadinessIncidentEvent


T = TypeVar("T", bound="MCPReadinessNotificationQueue")


@_attrs_define
class MCPReadinessNotificationQueue:
    """One readiness webhook rule and its full outstanding queue independent of incident health."""

    rule_id: UUID
    name: str
    enabled: bool
    outstanding_count: int
    blocked_count: int
    """Events newer than the oldest event; inspect destinations for actual receiver blocking."""
    oldest_age_seconds: float
    state: MCPReadinessNotificationQueueState
    can_retry: bool
    """Snapshot hint; the action transaction rechecks current eligibility."""
    can_abandon: bool
    """Whether the current snapshot permits permanent abandonment."""
    destinations: list[MCPReadinessDestinationQueue] | Unset = UNSET
    """Ordered receiver queues including fallback reservations awaiting the primary outcome."""
    mcp_readiness_scope: str | Unset = UNSET
    """Omitted for an all-environment rule."""
    failure_kind: MCPReadinessNotificationQueueFailureKind | Unset = UNSET
    """Safe failure classification without webhook URLs or remote response bodies."""
    head: MCPReadinessIncidentEvent | Unset = UNSET
    """Readiness incident transition or continuation with current notification status."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rule_id = str(self.rule_id)

        name = self.name

        enabled = self.enabled

        outstanding_count = self.outstanding_count

        blocked_count = self.blocked_count

        oldest_age_seconds = self.oldest_age_seconds

        state: str = self.state

        can_retry = self.can_retry

        can_abandon = self.can_abandon

        destinations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.destinations, Unset):
            destinations = []
            for destinations_item_data in self.destinations:
                destinations_item = destinations_item_data.to_dict()
                destinations.append(destinations_item)

        mcp_readiness_scope = self.mcp_readiness_scope

        failure_kind: str | Unset = UNSET
        if not isinstance(self.failure_kind, Unset):
            failure_kind = self.failure_kind

        head: dict[str, Any] | Unset = UNSET
        if not isinstance(self.head, Unset):
            head = self.head.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule_id": rule_id,
                "name": name,
                "enabled": enabled,
                "outstanding_count": outstanding_count,
                "blocked_count": blocked_count,
                "oldest_age_seconds": oldest_age_seconds,
                "state": state,
                "can_retry": can_retry,
                "can_abandon": can_abandon,
            }
        )
        if destinations is not UNSET:
            field_dict["destinations"] = destinations
        if mcp_readiness_scope is not UNSET:
            field_dict["mcp_readiness_scope"] = mcp_readiness_scope
        if failure_kind is not UNSET:
            field_dict["failure_kind"] = failure_kind
        if head is not UNSET:
            field_dict["head"] = head

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_destination_queue import MCPReadinessDestinationQueue
        from ..models.mcp_readiness_incident_event import MCPReadinessIncidentEvent

        d = dict(src_dict)
        rule_id = UUID(d.pop("rule_id"))

        name = d.pop("name")

        enabled = d.pop("enabled")

        outstanding_count = d.pop("outstanding_count")

        blocked_count = d.pop("blocked_count")

        oldest_age_seconds = d.pop("oldest_age_seconds")

        state = check_mcp_readiness_notification_queue_state(d.pop("state"))

        can_retry = d.pop("can_retry")

        can_abandon = d.pop("can_abandon")

        _destinations = d.pop("destinations", UNSET)
        destinations: list[MCPReadinessDestinationQueue] | Unset = UNSET
        if _destinations is not UNSET:
            destinations = []
            for destinations_item_data in _destinations:
                destinations_item = MCPReadinessDestinationQueue.from_dict(destinations_item_data)

                destinations.append(destinations_item)

        mcp_readiness_scope = d.pop("mcp_readiness_scope", UNSET)

        _failure_kind = d.pop("failure_kind", UNSET)
        failure_kind: MCPReadinessNotificationQueueFailureKind | Unset
        if isinstance(_failure_kind, Unset):
            failure_kind = UNSET
        else:
            failure_kind = check_mcp_readiness_notification_queue_failure_kind(_failure_kind)

        _head = d.pop("head", UNSET)
        head: MCPReadinessIncidentEvent | Unset
        if isinstance(_head, Unset):
            head = UNSET
        else:
            head = MCPReadinessIncidentEvent.from_dict(_head)

        mcp_readiness_notification_queue = cls(
            rule_id=rule_id,
            name=name,
            enabled=enabled,
            outstanding_count=outstanding_count,
            blocked_count=blocked_count,
            oldest_age_seconds=oldest_age_seconds,
            state=state,
            can_retry=can_retry,
            can_abandon=can_abandon,
            destinations=destinations,
            mcp_readiness_scope=mcp_readiness_scope,
            failure_kind=failure_kind,
            head=head,
        )

        mcp_readiness_notification_queue.additional_properties = d
        return mcp_readiness_notification_queue

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
