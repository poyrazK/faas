from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.environment_queue_smoke import EnvironmentQueueSmoke


T = TypeVar("T", bound="EnvironmentWorkloadQueueSmoke")


@_attrs_define
class EnvironmentWorkloadQueueSmoke:
    """Reviewed synthetic JSON input for each enabled push worker queue binding. Qualification sends it directly to the
    private candidate VM and never enqueues a customer message.

    """

    additional_properties: dict[str, EnvironmentQueueSmoke] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_queue_smoke import EnvironmentQueueSmoke

        d = dict(src_dict)
        environment_workload_queue_smoke = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = EnvironmentQueueSmoke.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        environment_workload_queue_smoke.additional_properties = additional_properties
        return environment_workload_queue_smoke

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> EnvironmentQueueSmoke:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: EnvironmentQueueSmoke) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
