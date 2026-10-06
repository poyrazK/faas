from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_bucket_versioning_desired_status import (
    ObjectBucketVersioningDesiredStatus,
    check_object_bucket_versioning_desired_status,
)
from ..models.object_bucket_versioning_observed_status import (
    ObjectBucketVersioningObservedStatus,
    check_object_bucket_versioning_observed_status,
)
from ..models.object_bucket_versioning_state import ObjectBucketVersioningState, check_object_bucket_versioning_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectBucketVersioning")


@_attrs_define
class ObjectBucketVersioning:
    """Desired and observed bucket versioning status with propagation and inventory progress."""

    bucket_id: UUID
    desired_status: ObjectBucketVersioningDesiredStatus
    observed_status: ObjectBucketVersioningObservedStatus
    state: ObjectBucketVersioningState
    revision: int
    versions_required: bool
    updated_at: datetime.datetime
    propagation_until: datetime.datetime | Unset = UNSET
    capacity_job_id: UUID | Unset = UNSET
    last_error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        desired_status: str = self.desired_status

        observed_status: str = self.observed_status

        state: str = self.state

        revision = self.revision

        versions_required = self.versions_required

        updated_at = self.updated_at.isoformat()

        propagation_until: str | Unset = UNSET
        if not isinstance(self.propagation_until, Unset):
            propagation_until = self.propagation_until.isoformat()

        capacity_job_id: str | Unset = UNSET
        if not isinstance(self.capacity_job_id, Unset):
            capacity_job_id = str(self.capacity_job_id)

        last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket_id": bucket_id,
                "desired_status": desired_status,
                "observed_status": observed_status,
                "state": state,
                "revision": revision,
                "versions_required": versions_required,
                "updated_at": updated_at,
            }
        )
        if propagation_until is not UNSET:
            field_dict["propagation_until"] = propagation_until
        if capacity_job_id is not UNSET:
            field_dict["capacity_job_id"] = capacity_job_id
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        desired_status = check_object_bucket_versioning_desired_status(d.pop("desired_status"))

        observed_status = check_object_bucket_versioning_observed_status(d.pop("observed_status"))

        state = check_object_bucket_versioning_state(d.pop("state"))

        revision = d.pop("revision")

        versions_required = d.pop("versions_required")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _propagation_until = d.pop("propagation_until", UNSET)
        propagation_until: datetime.datetime | Unset
        if isinstance(_propagation_until, Unset):
            propagation_until = UNSET
        else:
            propagation_until = datetime.datetime.fromisoformat(_propagation_until)

        _capacity_job_id = d.pop("capacity_job_id", UNSET)
        capacity_job_id: UUID | Unset
        if isinstance(_capacity_job_id, Unset):
            capacity_job_id = UNSET
        else:
            capacity_job_id = UUID(_capacity_job_id)

        last_error_code = d.pop("last_error_code", UNSET)

        object_bucket_versioning = cls(
            bucket_id=bucket_id,
            desired_status=desired_status,
            observed_status=observed_status,
            state=state,
            revision=revision,
            versions_required=versions_required,
            updated_at=updated_at,
            propagation_until=propagation_until,
            capacity_job_id=capacity_job_id,
            last_error_code=last_error_code,
        )

        object_bucket_versioning.additional_properties = d
        return object_bucket_versioning

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
