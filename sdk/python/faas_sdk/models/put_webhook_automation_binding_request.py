from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.put_webhook_automation_binding_request_filter import PutWebhookAutomationBindingRequestFilter


T = TypeVar("T", bound="PutWebhookAutomationBindingRequest")


@_attrs_define
class PutWebhookAutomationBindingRequest:
    """Bind a verified provider endpoint to a published automation with revision control."""

    expected_version: int
    workflow_name: str
    event_type: str
    take_over_delivery: bool
    filter_: PutWebhookAutomationBindingRequestFilter | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        workflow_name = self.workflow_name

        event_type = self.event_type

        take_over_delivery = self.take_over_delivery

        filter_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.filter_, Unset):
            filter_ = self.filter_.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "workflow_name": workflow_name,
                "event_type": event_type,
                "take_over_delivery": take_over_delivery,
            }
        )
        if filter_ is not UNSET:
            field_dict["filter"] = filter_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.put_webhook_automation_binding_request_filter import PutWebhookAutomationBindingRequestFilter

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        workflow_name = d.pop("workflow_name")

        event_type = d.pop("event_type")

        take_over_delivery = d.pop("take_over_delivery")

        _filter_ = d.pop("filter", UNSET)
        filter_: PutWebhookAutomationBindingRequestFilter | Unset
        if isinstance(_filter_, Unset):
            filter_ = UNSET
        else:
            filter_ = PutWebhookAutomationBindingRequestFilter.from_dict(_filter_)

        put_webhook_automation_binding_request = cls(
            expected_version=expected_version,
            workflow_name=workflow_name,
            event_type=event_type,
            take_over_delivery=take_over_delivery,
            filter_=filter_,
        )

        return put_webhook_automation_binding_request
