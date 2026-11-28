from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.preflight_level import PreflightLevel, check_preflight_level
from ..types import UNSET, Unset

T = TypeVar("T", bound="PreflightFinding")


@_attrs_define
class PreflightFinding:
    """One actionable observation about the source. `detail` says what was
    observed; `remedy` says what to change about it.

    """

    code: str
    """Stable machine-readable finding code."""
    level: PreflightLevel
    """`green` satisfies the container contract as written, `amber` needs a
    declared change, `red` cannot run.
    """
    title: str
    detail: str
    """What was observed in the source."""
    remedy: str | Unset = UNSET
    """What to change. Empty for informational findings."""
    sources: list[str] | Unset = UNSET
    """Repository-relative paths the finding came from. Never file contents."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        level: str = self.level

        title = self.title

        detail = self.detail

        remedy = self.remedy

        sources: list[str] | Unset = UNSET
        if not isinstance(self.sources, Unset):
            sources = self.sources

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "level": level,
                "title": title,
                "detail": detail,
            }
        )
        if remedy is not UNSET:
            field_dict["remedy"] = remedy
        if sources is not UNSET:
            field_dict["sources"] = sources

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        level = check_preflight_level(d.pop("level"))

        title = d.pop("title")

        detail = d.pop("detail")

        remedy = d.pop("remedy", UNSET)

        sources = cast(list[str], d.pop("sources", UNSET))

        preflight_finding = cls(
            code=code,
            level=level,
            title=title,
            detail=detail,
            remedy=remedy,
            sources=sources,
        )

        preflight_finding.additional_properties = d
        return preflight_finding

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
