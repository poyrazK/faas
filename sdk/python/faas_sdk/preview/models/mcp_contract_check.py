from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_contract_check_verdict import MCPContractCheckVerdict, check_mcp_contract_check_verdict
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_contract_change import MCPContractChange


T = TypeVar("T", bound="MCPContractCheck")


@_attrs_define
class MCPContractCheck:
    """One production candidate's comparison with the tool contract."""

    deployment_id: UUID
    verdict: MCPContractCheckVerdict
    baseline_sha256: str
    overridden: bool
    changes: list[MCPContractChange]
    checked_at: datetime.datetime
    contract_sha256: str | Unset = UNSET
    """Absent when the catalog could not be listed (unverified)."""
    accepted_at: datetime.datetime | Unset = UNSET
    """When the candidate went live."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        verdict: str = self.verdict

        baseline_sha256 = self.baseline_sha256

        overridden = self.overridden

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        checked_at = self.checked_at.isoformat()

        contract_sha256 = self.contract_sha256

        accepted_at: str | Unset = UNSET
        if not isinstance(self.accepted_at, Unset):
            accepted_at = self.accepted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "verdict": verdict,
                "baseline_sha256": baseline_sha256,
                "overridden": overridden,
                "changes": changes,
                "checked_at": checked_at,
            }
        )
        if contract_sha256 is not UNSET:
            field_dict["contract_sha256"] = contract_sha256
        if accepted_at is not UNSET:
            field_dict["accepted_at"] = accepted_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_contract_change import MCPContractChange

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        verdict = check_mcp_contract_check_verdict(d.pop("verdict"))

        baseline_sha256 = d.pop("baseline_sha256")

        overridden = d.pop("overridden")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = MCPContractChange.from_dict(changes_item_data)

            changes.append(changes_item)

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        contract_sha256 = d.pop("contract_sha256", UNSET)

        _accepted_at = d.pop("accepted_at", UNSET)
        accepted_at: datetime.datetime | Unset
        if isinstance(_accepted_at, Unset):
            accepted_at = UNSET
        else:
            accepted_at = datetime.datetime.fromisoformat(_accepted_at)

        mcp_contract_check = cls(
            deployment_id=deployment_id,
            verdict=verdict,
            baseline_sha256=baseline_sha256,
            overridden=overridden,
            changes=changes,
            checked_at=checked_at,
            contract_sha256=contract_sha256,
            accepted_at=accepted_at,
        )

        mcp_contract_check.additional_properties = d
        return mcp_contract_check

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
