from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.canary_preset_spec_preset import CanaryPresetSpecPreset, check_canary_preset_spec_preset
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.custom_stage import CustomStage


T = TypeVar("T", bound="CanaryPresetSpec")


@_attrs_define
class CanaryPresetSpec:
    """The canary ladder a customer asks for on a deploy (A + production-leveling
    Stream F). Preset is the catalog name (none/slow/balanced/aggressive/1-10-50-100/adaptive/
    custom). When Preset is 'custom', Stages is the
    customer-supplied ladder (each entry is percent +
    duration string in time.ParseDuration form, e.g.
    "1% at 30s, 10% at 2m, 100% at 0s").
    The wire-format change (StepDurations removed, Stages
    added) is additive on the consumer side because the
    prior StepDurations field was declared-but-dead — no
    pre-PR client ever sent it.

    """

    preset: CanaryPresetSpecPreset
    """Catalog preset name. 'none' = no canary (server stamps canary_preset='none', canary_total_steps=0). 'custom'
    requires Stages to be non-empty."""
    stages: list[CustomStage] | Unset = UNSET
    """Per-stage ladder. Required when preset='custom' (the API handler 422s otherwise); ignored for catalog
    presets (the catalog resolution runs server-side)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        preset: str = self.preset

        stages: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.stages, Unset):
            stages = []
            for stages_item_data in self.stages:
                stages_item = stages_item_data.to_dict()
                stages.append(stages_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "preset": preset,
            }
        )
        if stages is not UNSET:
            field_dict["stages"] = stages

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.custom_stage import CustomStage

        d = dict(src_dict)
        preset = check_canary_preset_spec_preset(d.pop("preset"))

        _stages = d.pop("stages", UNSET)
        stages: list[CustomStage] | Unset = UNSET
        if _stages is not UNSET:
            stages = []
            for stages_item_data in _stages:
                stages_item = CustomStage.from_dict(stages_item_data)

                stages.append(stages_item)

        canary_preset_spec = cls(
            preset=preset,
            stages=stages,
        )

        canary_preset_spec.additional_properties = d
        return canary_preset_spec

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
