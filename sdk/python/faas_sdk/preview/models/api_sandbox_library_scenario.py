from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_scenario import APISandboxScenario


T = TypeVar("T", bound="APISandboxLibraryScenario")


@_attrs_define
class APISandboxLibraryScenario:
    id: UUID
    scenario: APISandboxScenario
    source_request_id: UUID | Unset = UNSET
    """Private Debugger provenance of this scenario."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        scenario = self.scenario.to_dict()

        source_request_id: str | Unset = UNSET
        if not isinstance(self.source_request_id, Unset):
            source_request_id = str(self.source_request_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scenario": scenario,
            }
        )
        if source_request_id is not UNSET:
            field_dict["source_request_id"] = source_request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_scenario import APISandboxScenario

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        scenario = APISandboxScenario.from_dict(d.pop("scenario"))

        _source_request_id = d.pop("source_request_id", UNSET)
        source_request_id: UUID | Unset
        if isinstance(_source_request_id, Unset):
            source_request_id = UNSET
        else:
            source_request_id = UUID(_source_request_id)

        api_sandbox_library_scenario = cls(
            id=id,
            scenario=scenario,
            source_request_id=source_request_id,
        )

        api_sandbox_library_scenario.additional_properties = d
        return api_sandbox_library_scenario

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
