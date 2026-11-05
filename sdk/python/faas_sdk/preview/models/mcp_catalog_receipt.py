from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_receipt_state import MCPCatalogReceiptState, check_mcp_catalog_receipt_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_receipt_diff import MCPCatalogReceiptDiff


T = TypeVar("T", bound="MCPCatalogReceipt")


@_attrs_define
class MCPCatalogReceipt:
    """Retained catalog transition with server-computed drift, hashes and capture time."""

    id: UUID
    revision: UUID
    captured_at: datetime.datetime
    state: MCPCatalogReceiptState
    baseline_sha256: str
    changed_count: int
    truncated: bool
    role: str | Unset = "default"
    observed_sha256: str | Unset = UNSET
    diff: MCPCatalogReceiptDiff | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = str(self.revision)

        captured_at = self.captured_at.isoformat()

        state: str = self.state

        baseline_sha256 = self.baseline_sha256

        changed_count = self.changed_count

        truncated = self.truncated

        role = self.role

        observed_sha256 = self.observed_sha256

        diff: dict[str, Any] | Unset = UNSET
        if not isinstance(self.diff, Unset):
            diff = self.diff.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "captured_at": captured_at,
                "state": state,
                "baseline_sha256": baseline_sha256,
                "changed_count": changed_count,
                "truncated": truncated,
            }
        )
        if role is not UNSET:
            field_dict["role"] = role
        if observed_sha256 is not UNSET:
            field_dict["observed_sha256"] = observed_sha256
        if diff is not UNSET:
            field_dict["diff"] = diff

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_receipt_diff import MCPCatalogReceiptDiff

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = UUID(d.pop("revision"))

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        state = check_mcp_catalog_receipt_state(d.pop("state"))

        baseline_sha256 = d.pop("baseline_sha256")

        changed_count = d.pop("changed_count")

        truncated = d.pop("truncated")

        role = d.pop("role", UNSET)

        observed_sha256 = d.pop("observed_sha256", UNSET)

        _diff = d.pop("diff", UNSET)
        diff: MCPCatalogReceiptDiff | Unset
        if isinstance(_diff, Unset):
            diff = UNSET
        else:
            diff = MCPCatalogReceiptDiff.from_dict(_diff)

        mcp_catalog_receipt = cls(
            id=id,
            revision=revision,
            captured_at=captured_at,
            state=state,
            baseline_sha256=baseline_sha256,
            changed_count=changed_count,
            truncated=truncated,
            role=role,
            observed_sha256=observed_sha256,
            diff=diff,
        )

        mcp_catalog_receipt.additional_properties = d
        return mcp_catalog_receipt

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
