from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="APIConsumerPlanAssignmentResponse")


@_attrs_define
class APIConsumerPlanAssignmentResponse:
    """One append-only plan assignment; no plan_id is the default plan."""

    id: UUID
    consumer_id: UUID
    effective_from: datetime.datetime
    created_at: datetime.datetime
    plan_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        consumer_id = str(self.consumer_id)

        effective_from = self.effective_from.isoformat()

        created_at = self.created_at.isoformat()

        plan_id: str | Unset = UNSET
        if not isinstance(self.plan_id, Unset):
            plan_id = str(self.plan_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "consumer_id": consumer_id,
                "effective_from": effective_from,
                "created_at": created_at,
            }
        )
        if plan_id is not UNSET:
            field_dict["plan_id"] = plan_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        consumer_id = UUID(d.pop("consumer_id"))

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _plan_id = d.pop("plan_id", UNSET)
        plan_id: UUID | Unset
        if isinstance(_plan_id, Unset):
            plan_id = UNSET
        else:
            plan_id = UUID(_plan_id)

        api_consumer_plan_assignment_response = cls(
            id=id,
            consumer_id=consumer_id,
            effective_from=effective_from,
            created_at=created_at,
            plan_id=plan_id,
        )

        api_consumer_plan_assignment_response.additional_properties = d
        return api_consumer_plan_assignment_response

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
