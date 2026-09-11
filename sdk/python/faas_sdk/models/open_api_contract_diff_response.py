from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.open_api_contract_diff_response_source import (
    OpenAPIContractDiffResponseSource,
    check_open_api_contract_diff_response_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.open_api_contract_addition import OpenAPIContractAddition
    from ..models.open_api_contract_break import OpenAPIContractBreak


T = TypeVar("T", bound="OpenAPIContractDiffResponse")


@_attrs_define
class OpenAPIContractDiffResponse:
    """Read-only OpenAPI contract comparison for the authoritative imported
    app document (or the projected edge-rule fallback) and the latest
    captured deployment snapshot.
    `blocking` is true when a production promotion would be rejected
    while the contract-diff feature flag is enabled.

    """

    app_id: UUID
    scope: str
    source: OpenAPIContractDiffResponseSource
    """Contract source used for the proposed snapshot."""
    proposed_sha256: str
    blocking: bool
    breaks: list[OpenAPIContractBreak]
    additions: list[OpenAPIContractAddition]
    baseline_deployment_id: UUID | Unset = UNSET
    baseline_sha256: str | Unset = UNSET
    baseline_captured_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        source: str = self.source

        proposed_sha256 = self.proposed_sha256

        blocking = self.blocking

        breaks = []
        for breaks_item_data in self.breaks:
            breaks_item = breaks_item_data.to_dict()
            breaks.append(breaks_item)

        additions = []
        for additions_item_data in self.additions:
            additions_item = additions_item_data.to_dict()
            additions.append(additions_item)

        baseline_deployment_id: str | Unset = UNSET
        if not isinstance(self.baseline_deployment_id, Unset):
            baseline_deployment_id = str(self.baseline_deployment_id)

        baseline_sha256 = self.baseline_sha256

        baseline_captured_at: str | Unset = UNSET
        if not isinstance(self.baseline_captured_at, Unset):
            baseline_captured_at = self.baseline_captured_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "source": source,
                "proposed_sha256": proposed_sha256,
                "blocking": blocking,
                "breaks": breaks,
                "additions": additions,
            }
        )
        if baseline_deployment_id is not UNSET:
            field_dict["baseline_deployment_id"] = baseline_deployment_id
        if baseline_sha256 is not UNSET:
            field_dict["baseline_sha256"] = baseline_sha256
        if baseline_captured_at is not UNSET:
            field_dict["baseline_captured_at"] = baseline_captured_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.open_api_contract_addition import OpenAPIContractAddition
        from ..models.open_api_contract_break import OpenAPIContractBreak

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        source = check_open_api_contract_diff_response_source(d.pop("source"))

        proposed_sha256 = d.pop("proposed_sha256")

        blocking = d.pop("blocking")

        breaks = []
        _breaks = d.pop("breaks")
        for breaks_item_data in _breaks:
            breaks_item = OpenAPIContractBreak.from_dict(breaks_item_data)

            breaks.append(breaks_item)

        additions = []
        _additions = d.pop("additions")
        for additions_item_data in _additions:
            additions_item = OpenAPIContractAddition.from_dict(additions_item_data)

            additions.append(additions_item)

        _baseline_deployment_id = d.pop("baseline_deployment_id", UNSET)
        baseline_deployment_id: UUID | Unset
        if isinstance(_baseline_deployment_id, Unset):
            baseline_deployment_id = UNSET
        else:
            baseline_deployment_id = UUID(_baseline_deployment_id)

        baseline_sha256 = d.pop("baseline_sha256", UNSET)

        _baseline_captured_at = d.pop("baseline_captured_at", UNSET)
        baseline_captured_at: datetime.datetime | Unset
        if isinstance(_baseline_captured_at, Unset):
            baseline_captured_at = UNSET
        else:
            baseline_captured_at = datetime.datetime.fromisoformat(_baseline_captured_at)

        open_api_contract_diff_response = cls(
            app_id=app_id,
            scope=scope,
            source=source,
            proposed_sha256=proposed_sha256,
            blocking=blocking,
            breaks=breaks,
            additions=additions,
            baseline_deployment_id=baseline_deployment_id,
            baseline_sha256=baseline_sha256,
            baseline_captured_at=baseline_captured_at,
        )

        open_api_contract_diff_response.additional_properties = d
        return open_api_contract_diff_response

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
