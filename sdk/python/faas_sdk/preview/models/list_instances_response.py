from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.instance_response import InstanceResponse


T = TypeVar("T", bound="ListInstancesResponse")


@_attrs_define
class ListInstancesResponse:
    """Page shape for `GET /v1/instances` (issue #393). `instances`
    is the page in started_at DESC, id DESC order. `next_before`
    is the cursor the caller passes on the next request to fetch
    the older page; empty when the page is the end.

    """

    instances: list[InstanceResponse]
    next_before: None | Unset | UUID = UNSET
    """Cursor (instances.id UUIDv7) for the next older page. Empty / null at the end."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        instances = []
        for instances_item_data in self.instances:
            instances_item = instances_item_data.to_dict()
            instances.append(instances_item)

        next_before: None | str | Unset
        if isinstance(self.next_before, Unset):
            next_before = UNSET
        elif isinstance(self.next_before, UUID):
            next_before = str(self.next_before)
        else:
            next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "instances": instances,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.instance_response import InstanceResponse

        d = dict(src_dict)
        instances = []
        _instances = d.pop("instances")
        for instances_item_data in _instances:
            instances_item = InstanceResponse.from_dict(instances_item_data)

            instances.append(instances_item)

        def _parse_next_before(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_before_type_0 = UUID(data)

                return next_before_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        next_before = _parse_next_before(d.pop("next_before", UNSET))

        list_instances_response = cls(
            instances=instances,
            next_before=next_before,
        )

        list_instances_response.additional_properties = d
        return list_instances_response

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
