from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scenario_objects_data import ScenarioObjectsData
    from ..models.scenario_objects_spec import ScenarioObjectsSpec


T = TypeVar("T", bound="CreateScenarioObjectsRequest")


@_attrs_define
class CreateScenarioObjectsRequest:
    """Fresh primary workload and exact initial objects for an isolated rehearsal namespace."""

    app_slug: str
    spec: ScenarioObjectsSpec
    """Versioned single-bucket object rehearsal contract with a canonical fixture inventory digest."""
    objects: list[ScenarioObjectsData]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        spec = self.spec.to_dict()

        objects = []
        for objects_item_data in self.objects:
            objects_item = objects_item_data.to_dict()
            objects.append(objects_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "spec": spec,
                "objects": objects,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_objects_data import ScenarioObjectsData
        from ..models.scenario_objects_spec import ScenarioObjectsSpec

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        spec = ScenarioObjectsSpec.from_dict(d.pop("spec"))

        objects = []
        _objects = d.pop("objects")
        for objects_item_data in _objects:
            objects_item = ScenarioObjectsData.from_dict(objects_item_data)

            objects.append(objects_item)

        create_scenario_objects_request = cls(
            app_slug=app_slug,
            spec=spec,
            objects=objects,
        )

        create_scenario_objects_request.additional_properties = d
        return create_scenario_objects_request

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
