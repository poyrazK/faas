from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_receiver_invite import APISandboxReceiverInvite
    from ..models.api_sandbox_run_report import APISandboxRunReport


T = TypeVar("T", bound="APISandboxRunsResponse")


@_attrs_define
class APISandboxRunsResponse:
    """Recent scenario run reports for one release."""

    runs: list[APISandboxRunReport]
    receiver_invites: list[APISandboxReceiverInvite] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        runs = []
        for runs_item_data in self.runs:
            runs_item = runs_item_data.to_dict()
            runs.append(runs_item)

        receiver_invites: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.receiver_invites, Unset):
            receiver_invites = []
            for receiver_invites_item_data in self.receiver_invites:
                receiver_invites_item = receiver_invites_item_data.to_dict()
                receiver_invites.append(receiver_invites_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "runs": runs,
            }
        )
        if receiver_invites is not UNSET:
            field_dict["receiver_invites"] = receiver_invites

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_receiver_invite import APISandboxReceiverInvite
        from ..models.api_sandbox_run_report import APISandboxRunReport

        d = dict(src_dict)
        runs = []
        _runs = d.pop("runs")
        for runs_item_data in _runs:
            runs_item = APISandboxRunReport.from_dict(runs_item_data)

            runs.append(runs_item)

        _receiver_invites = d.pop("receiver_invites", UNSET)
        receiver_invites: list[APISandboxReceiverInvite] | Unset = UNSET
        if _receiver_invites is not UNSET:
            receiver_invites = []
            for receiver_invites_item_data in _receiver_invites:
                receiver_invites_item = APISandboxReceiverInvite.from_dict(receiver_invites_item_data)

                receiver_invites.append(receiver_invites_item)

        api_sandbox_runs_response = cls(
            runs=runs,
            receiver_invites=receiver_invites,
        )

        api_sandbox_runs_response.additional_properties = d
        return api_sandbox_runs_response

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
