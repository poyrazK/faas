from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_rule_coverage_set_id import ObsRuleCoverageSetId, check_obs_rule_coverage_set_id

T = TypeVar("T", bound="ObsRuleCoverageSet")


@_attrs_define
class ObsRuleCoverageSet:
    """Versioned minimum expectation set with a digest of its exact canonical definitions and safe source basenames."""

    id: ObsRuleCoverageSetId
    revision: str
    files: list[str]
    """Sorted safe source basenames selected by the expectation set."""
    expected_rules: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id: str = self.id

        revision = self.revision

        files = self.files

        expected_rules = self.expected_rules

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "files": files,
                "expected_rules": expected_rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = check_obs_rule_coverage_set_id(d.pop("id"))

        revision = d.pop("revision")

        files = cast(list[str], d.pop("files"))

        expected_rules = d.pop("expected_rules")

        obs_rule_coverage_set = cls(
            id=id,
            revision=revision,
            files=files,
            expected_rules=expected_rules,
        )

        obs_rule_coverage_set.additional_properties = d
        return obs_rule_coverage_set

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
