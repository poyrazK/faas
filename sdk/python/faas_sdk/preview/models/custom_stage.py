from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mirror_clean_condition import MirrorCleanCondition


T = TypeVar("T", bound="CustomStage")


@_attrs_define
class CustomStage:
    """One stage of a customer-supplied canary ladder
    (issue #976 / ADR-122 / SAFE-RELEASES production-leveling
    Stream F). Percent is the traffic share this stage moves
    to (0..100, terminal stage must be 100). Duration is the
    wall-clock dwell time at this stage in time.ParseDuration
    form (e.g. "30s", "2m", "0s" for the terminal hop).

    """

    percent: int
    """Traffic share this stage moves to (0..100). The terminal stage must be 100."""
    duration: str
    """Wall-clock dwell at this stage, in time.ParseDuration form (e.g. '30s', '2m'). '0s' for the terminal hop."""
    mirror_clean: MirrorCleanCondition | None | Unset = UNSET
    """Require a clean traffic-mirror window before advancing out of this stage. Any status, schema, body, or crash
    diff aborts the rollout."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.mirror_clean_condition import MirrorCleanCondition

        percent = self.percent

        duration = self.duration

        mirror_clean: dict[str, Any] | None | Unset
        if isinstance(self.mirror_clean, Unset):
            mirror_clean = UNSET
        elif isinstance(self.mirror_clean, MirrorCleanCondition):
            mirror_clean = self.mirror_clean.to_dict()
        else:
            mirror_clean = self.mirror_clean

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "percent": percent,
                "duration": duration,
            }
        )
        if mirror_clean is not UNSET:
            field_dict["mirror_clean"] = mirror_clean

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mirror_clean_condition import MirrorCleanCondition

        d = dict(src_dict)
        percent = d.pop("percent")

        duration = d.pop("duration")

        def _parse_mirror_clean(data: object) -> MirrorCleanCondition | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                mirror_clean_type_0 = MirrorCleanCondition.from_dict(data)

                return mirror_clean_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MirrorCleanCondition | None | Unset, data)

        mirror_clean = _parse_mirror_clean(d.pop("mirror_clean", UNSET))

        custom_stage = cls(
            percent=percent,
            duration=duration,
            mirror_clean=mirror_clean,
        )

        custom_stage.additional_properties = d
        return custom_stage

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
