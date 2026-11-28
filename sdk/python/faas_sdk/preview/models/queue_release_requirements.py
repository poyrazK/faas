from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_release_requirements_additional_property import (
    QueueReleaseRequirementsAdditionalProperty,
    check_queue_release_requirements_additional_property,
)

T = TypeVar("T", bound="QueueReleaseRequirements")


@_attrs_define
class QueueReleaseRequirements:
    """Binding name to explicit required queue evidence. Names match ^[a-z][a-z0-9-]{0,62}$. consumer_readiness requires
    one enabled push consumer with a healthy poll no older than thirty seconds; connectivity coverage remains partial.

    """

    additional_properties: dict[str, QueueReleaseRequirementsAdditionalProperty] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        queue_release_requirements = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = check_queue_release_requirements_additional_property(prop_dict)

            additional_properties[prop_name] = additional_property

        queue_release_requirements.additional_properties = additional_properties
        return queue_release_requirements

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> QueueReleaseRequirementsAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: QueueReleaseRequirementsAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
