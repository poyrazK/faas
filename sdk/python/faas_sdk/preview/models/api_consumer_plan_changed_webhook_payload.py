from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerPlanChangedWebhookPayload")


@_attrs_define
class APIConsumerPlanChangedWebhookPayload:
    """Data delivered with consumer.plan_changed when a customer switched plans with their own key. assignment_id is stable
    across retries; an omitted plan is the app's default plan.

    """

    assignment_id: UUID
    app_id: UUID
    consumer_id: UUID
    external_ref: str
    effective_from: datetime.datetime
    changed_at: datetime.datetime
    from_plan_id: UUID | Unset = UNSET
    to_plan_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        assignment_id = str(self.assignment_id)

        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        external_ref = self.external_ref

        effective_from = self.effective_from.isoformat()

        changed_at = self.changed_at.isoformat()

        from_plan_id: str | Unset = UNSET
        if not isinstance(self.from_plan_id, Unset):
            from_plan_id = str(self.from_plan_id)

        to_plan_id: str | Unset = UNSET
        if not isinstance(self.to_plan_id, Unset):
            to_plan_id = str(self.to_plan_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "assignment_id": assignment_id,
                "app_id": app_id,
                "consumer_id": consumer_id,
                "external_ref": external_ref,
                "effective_from": effective_from,
                "changed_at": changed_at,
            }
        )
        if from_plan_id is not UNSET:
            field_dict["from_plan_id"] = from_plan_id
        if to_plan_id is not UNSET:
            field_dict["to_plan_id"] = to_plan_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        assignment_id = UUID(d.pop("assignment_id"))

        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        external_ref = d.pop("external_ref")

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        changed_at = datetime.datetime.fromisoformat(d.pop("changed_at"))

        _from_plan_id = d.pop("from_plan_id", UNSET)
        from_plan_id: UUID | Unset
        if isinstance(_from_plan_id, Unset):
            from_plan_id = UNSET
        else:
            from_plan_id = UUID(_from_plan_id)

        _to_plan_id = d.pop("to_plan_id", UNSET)
        to_plan_id: UUID | Unset
        if isinstance(_to_plan_id, Unset):
            to_plan_id = UNSET
        else:
            to_plan_id = UUID(_to_plan_id)

        api_consumer_plan_changed_webhook_payload = cls(
            assignment_id=assignment_id,
            app_id=app_id,
            consumer_id=consumer_id,
            external_ref=external_ref,
            effective_from=effective_from,
            changed_at=changed_at,
            from_plan_id=from_plan_id,
            to_plan_id=to_plan_id,
        )

        api_consumer_plan_changed_webhook_payload.additional_properties = d
        return api_consumer_plan_changed_webhook_payload

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
