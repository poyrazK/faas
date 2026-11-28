from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_diagnostic_group import ObsRuntimeDiagnosticGroup, check_obs_runtime_diagnostic_group
from ..models.obs_runtime_diagnostics_scope import ObsRuntimeDiagnosticsScope, check_obs_runtime_diagnostics_scope

T = TypeVar("T", bound="ObsRuntimeDiagnostics")


@_attrs_define
class ObsRuntimeDiagnostics:
    """Explicit source-side selection acknowledgement. Absent on default requests to preserve compatibility. Selected and
    not_selected are disjoint canonical subsequences partitioning the groups supported by the role. A selected group can
    still be unavailable; a not_selected group is intentionally omitted. Selection never suppresses core identity,
    readiness, process or loop evidence, expands a budget, or changes a deployment gate.

    """

    scope: ObsRuntimeDiagnosticsScope
    selected: list[ObsRuntimeDiagnosticGroup]
    not_selected: list[ObsRuntimeDiagnosticGroup]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        selected = []
        for selected_item_data in self.selected:
            selected_item: str = selected_item_data
            selected.append(selected_item)

        not_selected = []
        for not_selected_item_data in self.not_selected:
            not_selected_item: str = not_selected_item_data
            not_selected.append(not_selected_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "selected": selected,
                "not_selected": not_selected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = check_obs_runtime_diagnostics_scope(d.pop("scope"))

        selected = []
        _selected = d.pop("selected")
        for selected_item_data in _selected:
            selected_item = check_obs_runtime_diagnostic_group(selected_item_data)

            selected.append(selected_item)

        not_selected = []
        _not_selected = d.pop("not_selected")
        for not_selected_item_data in _not_selected:
            not_selected_item = check_obs_runtime_diagnostic_group(not_selected_item_data)

            not_selected.append(not_selected_item)

        obs_runtime_diagnostics = cls(
            scope=scope,
            selected=selected,
            not_selected=not_selected,
        )

        obs_runtime_diagnostics.additional_properties = d
        return obs_runtime_diagnostics

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
