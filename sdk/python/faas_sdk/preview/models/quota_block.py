from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="QuotaBlock")


@_attrs_define
class QuotaBlock:
    """Limit + observed extension on a plan-quota problem. Mirrors
    api.Problem.WithLimit; emitted alongside any 402/403 quota
    response so the CLI can render "X/Y apps" without a second
    request.

    """

    limit: int | Unset = UNSET
    observed: int | Unset = UNSET
    docs_url: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        limit = self.limit

        observed = self.observed

        docs_url = self.docs_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if limit is not UNSET:
            field_dict["limit"] = limit
        if observed is not UNSET:
            field_dict["observed"] = observed
        if docs_url is not UNSET:
            field_dict["docs_url"] = docs_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        limit = d.pop("limit", UNSET)

        observed = d.pop("observed", UNSET)

        docs_url = d.pop("docs_url", UNSET)

        quota_block = cls(
            limit=limit,
            observed=observed,
            docs_url=docs_url,
        )

        quota_block.additional_properties = d
        return quota_block

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
