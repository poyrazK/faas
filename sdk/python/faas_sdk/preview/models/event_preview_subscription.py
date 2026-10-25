from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_preview_subscription_filter import EventPreviewSubscriptionFilter


T = TypeVar("T", bound="EventPreviewSubscription")


@_attrs_define
class EventPreviewSubscription:
    """A bounded sample of an enabled subscription or workflow considered by the event router."""

    app_slug: str
    subscription_id: UUID
    source: str
    type_: str
    filter_: EventPreviewSubscriptionFilter
    """Normalized content filter declared by this subscription."""
    reason: str
    """would_deliver, content_filter_mismatch, pattern_mismatch, tenant_mismatch, or an invalid_subscription
    explanation."""
    schema_versions: list[str] | Unset = UNSET
    """Exact case-sensitive schema versions. Empty or omitted accepts all versions; a nonempty selection excludes
    unversioned events. Selection is captured at publication or backfill creation."""
    workflow_name: str | Unset = UNSET
    """Present for workflow recipients."""
    deployment_id: UUID | Unset = UNSET
    """Acceptance candidate deployment for workflow recipients."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        subscription_id = str(self.subscription_id)

        source = self.source

        type_ = self.type_

        filter_ = self.filter_.to_dict()

        reason = self.reason

        schema_versions: list[str] | Unset = UNSET
        if not isinstance(self.schema_versions, Unset):
            schema_versions = self.schema_versions

        workflow_name = self.workflow_name

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "subscription_id": subscription_id,
                "source": source,
                "type": type_,
                "filter": filter_,
                "reason": reason,
            }
        )
        if schema_versions is not UNSET:
            field_dict["schema_versions"] = schema_versions
        if workflow_name is not UNSET:
            field_dict["workflow_name"] = workflow_name
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_preview_subscription_filter import EventPreviewSubscriptionFilter

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        subscription_id = UUID(d.pop("subscription_id"))

        source = d.pop("source")

        type_ = d.pop("type")

        filter_ = EventPreviewSubscriptionFilter.from_dict(d.pop("filter"))

        reason = d.pop("reason")

        schema_versions = cast(list[str], d.pop("schema_versions", UNSET))

        workflow_name = d.pop("workflow_name", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        event_preview_subscription = cls(
            app_slug=app_slug,
            subscription_id=subscription_id,
            source=source,
            type_=type_,
            filter_=filter_,
            reason=reason,
            schema_versions=schema_versions,
            workflow_name=workflow_name,
            deployment_id=deployment_id,
        )

        return event_preview_subscription
