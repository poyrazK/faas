from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_branch_bucket_consistency import (
    ProjectEnvironmentBranchBucketConsistency,
    check_project_environment_branch_bucket_consistency,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentBranchBucket")


@_attrs_define
class ProjectEnvironmentBranchBucket:
    """One overlay bucket a branch owns on a parent bucket. The branch reads every key it does not hold itself from the
    parent.

    """

    source_bucket_id: str
    name: str | Unset = UNSET
    """The parent bucket's name."""
    copy_bucket_id: str | Unset = UNSET
    """Empty until the bucket is made."""
    consistency: ProjectEnvironmentBranchBucketConsistency | Unset = UNSET
    """as_of when the branch reads the parent as of the branch point (the parent had versioning on when the branch
    was made); live_parent when it reads the parent's current objects."""
    as_of: datetime.datetime | Unset = UNSET
    """The instant an as_of bucket reads the parent at."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_bucket_id = self.source_bucket_id

        name = self.name

        copy_bucket_id = self.copy_bucket_id

        consistency: str | Unset = UNSET
        if not isinstance(self.consistency, Unset):
            consistency = self.consistency

        as_of: str | Unset = UNSET
        if not isinstance(self.as_of, Unset):
            as_of = self.as_of.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_bucket_id": source_bucket_id,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name
        if copy_bucket_id is not UNSET:
            field_dict["copy_bucket_id"] = copy_bucket_id
        if consistency is not UNSET:
            field_dict["consistency"] = consistency
        if as_of is not UNSET:
            field_dict["as_of"] = as_of

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_bucket_id = d.pop("source_bucket_id")

        name = d.pop("name", UNSET)

        copy_bucket_id = d.pop("copy_bucket_id", UNSET)

        _consistency = d.pop("consistency", UNSET)
        consistency: ProjectEnvironmentBranchBucketConsistency | Unset
        if isinstance(_consistency, Unset):
            consistency = UNSET
        else:
            consistency = check_project_environment_branch_bucket_consistency(_consistency)

        _as_of = d.pop("as_of", UNSET)
        as_of: datetime.datetime | Unset
        if isinstance(_as_of, Unset):
            as_of = UNSET
        else:
            as_of = datetime.datetime.fromisoformat(_as_of)

        project_environment_branch_bucket = cls(
            source_bucket_id=source_bucket_id,
            name=name,
            copy_bucket_id=copy_bucket_id,
            consistency=consistency,
            as_of=as_of,
        )

        project_environment_branch_bucket.additional_properties = d
        return project_environment_branch_bucket

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
