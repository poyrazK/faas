from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_investigation_response import ProfileInvestigationResponse


T = TypeVar("T", bound="ListProfileInvestigationsResponse")


@_attrs_define
class ListProfileInvestigationsResponse:
    """Saved investigations ordered by most recent update, then UUID."""

    investigations: list[ProfileInvestigationResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        investigations = []
        for investigations_item_data in self.investigations:
            investigations_item = investigations_item_data.to_dict()
            investigations.append(investigations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "investigations": investigations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_investigation_response import ProfileInvestigationResponse

        d = dict(src_dict)
        investigations = []
        _investigations = d.pop("investigations")
        for investigations_item_data in _investigations:
            investigations_item = ProfileInvestigationResponse.from_dict(investigations_item_data)

            investigations.append(investigations_item)

        list_profile_investigations_response = cls(
            investigations=investigations,
        )

        list_profile_investigations_response.additional_properties = d
        return list_profile_investigations_response

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
