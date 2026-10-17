from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_objects_spec_version import ScenarioObjectsSpecVersion, check_scenario_objects_spec_version

T = TypeVar("T", bound="ScenarioObjectsSpec")


@_attrs_define
class ScenarioObjectsSpec:
    """Versioned single-bucket object rehearsal contract with a canonical fixture inventory digest."""

    version: ScenarioObjectsSpecVersion
    bucket: str
    fixture_sha256: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        bucket = self.bucket

        fixture_sha256 = self.fixture_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "bucket": bucket,
                "fixture_sha256": fixture_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_scenario_objects_spec_version(d.pop("version"))

        bucket = d.pop("bucket")

        fixture_sha256 = d.pop("fixture_sha256")

        scenario_objects_spec = cls(
            version=version,
            bucket=bucket,
            fixture_sha256=fixture_sha256,
        )

        scenario_objects_spec.additional_properties = d
        return scenario_objects_spec

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
