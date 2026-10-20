from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.data_upstream_response import DataUpstreamResponse


T = TypeVar("T", bound="DataUpstreamListResponse")


@_attrs_define
class DataUpstreamListResponse:
    """List response wrapper. `quota_max` is the per-plan cap from
    `pkg/api/limits.go::DataPlacementHintsPerApp`; `count` is the
    number of rows in `upstreams` (may be less than `quota_max`).

    """

    upstreams: list[DataUpstreamResponse]
    quota_max: int
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        upstreams = []
        for upstreams_item_data in self.upstreams:
            upstreams_item = upstreams_item_data.to_dict()
            upstreams.append(upstreams_item)

        quota_max = self.quota_max

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "upstreams": upstreams,
                "quota_max": quota_max,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_upstream_response import DataUpstreamResponse

        d = dict(src_dict)
        upstreams = []
        _upstreams = d.pop("upstreams")
        for upstreams_item_data in _upstreams:
            upstreams_item = DataUpstreamResponse.from_dict(upstreams_item_data)

            upstreams.append(upstreams_item)

        quota_max = d.pop("quota_max")

        count = d.pop("count")

        data_upstream_list_response = cls(
            upstreams=upstreams,
            quota_max=quota_max,
            count=count,
        )

        data_upstream_list_response.additional_properties = d
        return data_upstream_list_response

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
