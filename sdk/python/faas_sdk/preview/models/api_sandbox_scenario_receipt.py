from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_example_request import APISandboxExampleRequest
    from ..models.api_sandbox_scenario_guide_seeded_ids import APISandboxScenarioGuideSeededIds


T = TypeVar("T", bound="APISandboxScenarioReceipt")


@_attrs_define
class APISandboxScenarioReceipt:
    name: str
    requests: list[APISandboxExampleRequest]
    revision: UUID
    seeded_ids: APISandboxScenarioGuideSeededIds | Unset = UNSET
    """Synthetic IDs shown to invite recipients."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        revision = str(self.revision)

        seeded_ids: dict[str, Any] | Unset = UNSET
        if not isinstance(self.seeded_ids, Unset):
            seeded_ids = self.seeded_ids.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "requests": requests,
                "revision": revision,
            }
        )
        if seeded_ids is not UNSET:
            field_dict["seeded_ids"] = seeded_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_example_request import APISandboxExampleRequest
        from ..models.api_sandbox_scenario_guide_seeded_ids import APISandboxScenarioGuideSeededIds

        d = dict(src_dict)
        name = d.pop("name")

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = APISandboxExampleRequest.from_dict(requests_item_data)

            requests.append(requests_item)

        revision = UUID(d.pop("revision"))

        _seeded_ids = d.pop("seeded_ids", UNSET)
        seeded_ids: APISandboxScenarioGuideSeededIds | Unset
        if isinstance(_seeded_ids, Unset):
            seeded_ids = UNSET
        else:
            seeded_ids = APISandboxScenarioGuideSeededIds.from_dict(_seeded_ids)

        api_sandbox_scenario_receipt = cls(
            name=name,
            requests=requests,
            revision=revision,
            seeded_ids=seeded_ids,
        )

        api_sandbox_scenario_receipt.additional_properties = d
        return api_sandbox_scenario_receipt

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
