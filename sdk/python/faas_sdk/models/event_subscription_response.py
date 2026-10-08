from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_subscription_response_work_action import (
    EventSubscriptionResponseWorkAction,
    check_event_subscription_response_work_action,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_routing_retry_policy import EventRoutingRetryPolicy
    from ..models.event_subscription_response_filter import EventSubscriptionResponseFilter


T = TypeVar("T", bound="EventSubscriptionResponse")


@_attrs_define
class EventSubscriptionResponse:
    """One manifest-declared event subscription reconciled for an app."""

    id: UUID
    app_id: UUID
    source: str
    """Event source pattern, including supported wildcard forms."""
    type_: str
    """Event type pattern, including supported wildcard forms."""
    filter_: EventSubscriptionResponseFilter
    """Normalized JSON filter evaluated by the event matcher."""
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    schema_versions: list[str] | Unset = UNSET
    """Exact case-sensitive schema versions. Empty or omitted accepts all versions; a nonempty selection excludes
    unversioned events. Selection is captured at publication or backfill creation."""
    routing_retry_policy: EventRoutingRetryPolicy | Unset = UNSET
    """Routing policy before invocation admission. Duration budgets include routing attempt time and scheduled
    retry delays. Admission waits add no budget cost. API replacement accepts explicit settings; manifests and CLI
    default configured policies to jitter enabled."""
    work_policy: str | Unset = UNSET
    """Named app policy for keyed event deliveries, when configured."""
    work_key: str | Unset = UNSET
    """Dot selector into the CloudEvents envelope for the work key."""
    work_fairness_key: str | Unset = UNSET
    """Optional dot selector for an application fairness group; defaults to work_key."""
    work_action: EventSubscriptionResponseWorkAction | Unset = UNSET
    """Action taken on a matching event."""
    ordered: bool | Unset = UNSET
    """Whether this subscription waits for earlier matching keyed deliveries before routing."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        source = self.source

        type_ = self.type_

        filter_ = self.filter_.to_dict()

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        schema_versions: list[str] | Unset = UNSET
        if not isinstance(self.schema_versions, Unset):
            schema_versions = self.schema_versions

        routing_retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.routing_retry_policy, Unset):
            routing_retry_policy = self.routing_retry_policy.to_dict()

        work_policy = self.work_policy

        work_key = self.work_key

        work_fairness_key = self.work_fairness_key

        work_action: str | Unset = UNSET
        if not isinstance(self.work_action, Unset):
            work_action = self.work_action

        ordered = self.ordered

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "source": source,
                "type": type_,
                "filter": filter_,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if schema_versions is not UNSET:
            field_dict["schema_versions"] = schema_versions
        if routing_retry_policy is not UNSET:
            field_dict["routing_retry_policy"] = routing_retry_policy
        if work_policy is not UNSET:
            field_dict["work_policy"] = work_policy
        if work_key is not UNSET:
            field_dict["work_key"] = work_key
        if work_fairness_key is not UNSET:
            field_dict["work_fairness_key"] = work_fairness_key
        if work_action is not UNSET:
            field_dict["work_action"] = work_action
        if ordered is not UNSET:
            field_dict["ordered"] = ordered

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_routing_retry_policy import EventRoutingRetryPolicy
        from ..models.event_subscription_response_filter import EventSubscriptionResponseFilter

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        type_ = d.pop("type")

        filter_ = EventSubscriptionResponseFilter.from_dict(d.pop("filter"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        schema_versions = cast(list[str], d.pop("schema_versions", UNSET))

        _routing_retry_policy = d.pop("routing_retry_policy", UNSET)
        routing_retry_policy: EventRoutingRetryPolicy | Unset
        if isinstance(_routing_retry_policy, Unset):
            routing_retry_policy = UNSET
        else:
            routing_retry_policy = EventRoutingRetryPolicy.from_dict(_routing_retry_policy)

        work_policy = d.pop("work_policy", UNSET)

        work_key = d.pop("work_key", UNSET)

        work_fairness_key = d.pop("work_fairness_key", UNSET)

        _work_action = d.pop("work_action", UNSET)
        work_action: EventSubscriptionResponseWorkAction | Unset
        if isinstance(_work_action, Unset):
            work_action = UNSET
        else:
            work_action = check_event_subscription_response_work_action(_work_action)

        ordered = d.pop("ordered", UNSET)

        event_subscription_response = cls(
            id=id,
            app_id=app_id,
            source=source,
            type_=type_,
            filter_=filter_,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
            schema_versions=schema_versions,
            routing_retry_policy=routing_retry_policy,
            work_policy=work_policy,
            work_key=work_key,
            work_fairness_key=work_fairness_key,
            work_action=work_action,
            ordered=ordered,
        )

        return event_subscription_response
