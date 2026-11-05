from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ReleaseDiffSide")


@_attrs_define
class ReleaseDiffSide:
    """One of the two compared releases."""

    deployment_id: UUID
    created_at: datetime.datetime
    revision: int | Unset = UNSET
    deployed_by: str | Unset = UNSET
    reason: str | Unset = UNSET
    tag: str | Unset = UNSET
    pr_number: int | Unset = UNSET
    commit: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        created_at = self.created_at.isoformat()

        revision = self.revision

        deployed_by = self.deployed_by

        reason = self.reason

        tag = self.tag

        pr_number = self.pr_number

        commit = self.commit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "created_at": created_at,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision
        if deployed_by is not UNSET:
            field_dict["deployed_by"] = deployed_by
        if reason is not UNSET:
            field_dict["reason"] = reason
        if tag is not UNSET:
            field_dict["tag"] = tag
        if pr_number is not UNSET:
            field_dict["pr_number"] = pr_number
        if commit is not UNSET:
            field_dict["commit"] = commit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        revision = d.pop("revision", UNSET)

        deployed_by = d.pop("deployed_by", UNSET)

        reason = d.pop("reason", UNSET)

        tag = d.pop("tag", UNSET)

        pr_number = d.pop("pr_number", UNSET)

        commit = d.pop("commit", UNSET)

        release_diff_side = cls(
            deployment_id=deployment_id,
            created_at=created_at,
            revision=revision,
            deployed_by=deployed_by,
            reason=reason,
            tag=tag,
            pr_number=pr_number,
            commit=commit,
        )

        release_diff_side.additional_properties = d
        return release_diff_side

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
