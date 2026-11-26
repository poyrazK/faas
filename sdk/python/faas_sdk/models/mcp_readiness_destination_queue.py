from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_destination_queue_destination import (
    MCPReadinessDestinationQueueDestination,
    check_mcp_readiness_destination_queue_destination,
)
from ..models.mcp_readiness_destination_queue_failure_kind import (
    MCPReadinessDestinationQueueFailureKind,
    check_mcp_readiness_destination_queue_failure_kind,
)
from ..models.mcp_readiness_destination_queue_provider import (
    MCPReadinessDestinationQueueProvider,
    check_mcp_readiness_destination_queue_provider,
)
from ..models.mcp_readiness_destination_queue_state import (
    MCPReadinessDestinationQueueState,
    check_mcp_readiness_destination_queue_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_incident_event import MCPReadinessIncidentEvent


T = TypeVar("T", bound="MCPReadinessDestinationQueue")


@_attrs_define
class MCPReadinessDestinationQueue:
    """One ordered receiver lane; waiting_primary reserves order until primary succeeds or fails over."""

    destination: MCPReadinessDestinationQueueDestination
    outstanding_count: int
    blocked_count: int
    state: MCPReadinessDestinationQueueState
    can_retry: bool
    can_abandon: bool
    provider: MCPReadinessDestinationQueueProvider | Unset = UNSET
    """Native transport of this queue head; omitted for an idle receiver or legacy inspection."""
    failure_kind: MCPReadinessDestinationQueueFailureKind | Unset = UNSET
    head: MCPReadinessIncidentEvent | Unset = UNSET
    """Readiness incident transition or continuation with current notification status."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        destination: str = self.destination

        outstanding_count = self.outstanding_count

        blocked_count = self.blocked_count

        state: str = self.state

        can_retry = self.can_retry

        can_abandon = self.can_abandon

        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

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
                "destination": destination,
                "outstanding_count": outstanding_count,
                "blocked_count": blocked_count,
                "state": state,
                "can_retry": can_retry,
                "can_abandon": can_abandon,
            }
        )
        if provider is not UNSET:
            field_dict["provider"] = provider
        if failure_kind is not UNSET:
            field_dict["failure_kind"] = failure_kind
        if head is not UNSET:
            field_dict["head"] = head

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_incident_event import MCPReadinessIncidentEvent

        d = dict(src_dict)
        destination = check_mcp_readiness_destination_queue_destination(d.pop("destination"))

        outstanding_count = d.pop("outstanding_count")

        blocked_count = d.pop("blocked_count")

        state = check_mcp_readiness_destination_queue_state(d.pop("state"))

        can_retry = d.pop("can_retry")

        can_abandon = d.pop("can_abandon")

        _provider = d.pop("provider", UNSET)
        provider: MCPReadinessDestinationQueueProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_mcp_readiness_destination_queue_provider(_provider)

        _failure_kind = d.pop("failure_kind", UNSET)
        failure_kind: MCPReadinessDestinationQueueFailureKind | Unset
        if isinstance(_failure_kind, Unset):
            failure_kind = UNSET
        else:
            failure_kind = check_mcp_readiness_destination_queue_failure_kind(_failure_kind)

        _head = d.pop("head", UNSET)
        head: MCPReadinessIncidentEvent | Unset
        if isinstance(_head, Unset):
            head = UNSET
        else:
            head = MCPReadinessIncidentEvent.from_dict(_head)

        mcp_readiness_destination_queue = cls(
            destination=destination,
            outstanding_count=outstanding_count,
            blocked_count=blocked_count,
            state=state,
            can_retry=can_retry,
            can_abandon=can_abandon,
            provider=provider,
            failure_kind=failure_kind,
            head=head,
        )

        mcp_readiness_destination_queue.additional_properties = d
        return mcp_readiness_destination_queue

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
