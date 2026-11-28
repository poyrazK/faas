from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.activity_actor_response_type import ActivityActorResponseType, check_activity_actor_response_type
from ..types import UNSET, Unset

T = TypeVar("T", bound="ActivityActorResponse")


@_attrs_define
class ActivityActorResponse:
    """Captured identity responsible for an activity item."""

    type_: ActivityActorResponseType
    label: str
    """Actor name captured when the activity occurred."""
    account_id: UUID | Unset = UNSET
    """Present for a locally-known human actor."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        label = self.label

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "label": label,
            }
        )
        if account_id is not UNSET:
            field_dict["account_id"] = account_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_activity_actor_response_type(d.pop("type"))

        label = d.pop("label")

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        activity_actor_response = cls(
            type_=type_,
            label=label,
            account_id=account_id,
        )

        activity_actor_response.additional_properties = d
        return activity_actor_response

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
