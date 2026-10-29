from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.object_bucket_object_lock_last_error_code import (
    ObjectBucketObjectLockLastErrorCode,
    check_object_bucket_object_lock_last_error_code,
)
from ..models.object_bucket_object_lock_state import ObjectBucketObjectLockState, check_object_bucket_object_lock_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_bucket_object_lock_configuration import ObjectBucketObjectLockConfiguration


T = TypeVar("T", bound="ObjectBucketObjectLock")


@_attrs_define
class ObjectBucketObjectLock:
    """Owned durable intent and native observation with permanent enablement history and reconciliation progress."""

    bucket_id: UUID
    state: ObjectBucketObjectLockState
    revision: int
    enabled_required: bool
    """Permanent protection latch; enablement and version accounting cannot be disabled."""
    observed_known: bool
    updated_at: datetime.datetime
    observed_configuration: ObjectBucketObjectLockConfiguration | Unset = UNSET
    """Native observation may report disabled. PUT requires enabled true. Omitting default_retention clears
    defaults while keeping Object Lock enabled."""
    desired_configuration: ObjectBucketObjectLockConfiguration | Unset = UNSET
    """Native observation may report disabled. PUT requires enabled true. Omitting default_retention clears
    defaults while keeping Object Lock enabled."""
    last_error_code: ObjectBucketObjectLockLastErrorCode | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        state: str = self.state

        revision = self.revision

        enabled_required = self.enabled_required

        observed_known = self.observed_known

        updated_at = self.updated_at.isoformat()

        observed_configuration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observed_configuration, Unset):
            observed_configuration = self.observed_configuration.to_dict()

        desired_configuration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.desired_configuration, Unset):
            desired_configuration = self.desired_configuration.to_dict()

        last_error_code: str | Unset = UNSET
        if not isinstance(self.last_error_code, Unset):
            last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_id": bucket_id,
                "state": state,
                "revision": revision,
                "enabled_required": enabled_required,
                "observed_known": observed_known,
                "updated_at": updated_at,
            }
        )
        if observed_configuration is not UNSET:
            field_dict["observed_configuration"] = observed_configuration
        if desired_configuration is not UNSET:
            field_dict["desired_configuration"] = desired_configuration
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_bucket_object_lock_configuration import ObjectBucketObjectLockConfiguration

        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        state = check_object_bucket_object_lock_state(d.pop("state"))

        revision = d.pop("revision")

        enabled_required = d.pop("enabled_required")

        observed_known = d.pop("observed_known")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _observed_configuration = d.pop("observed_configuration", UNSET)
        observed_configuration: ObjectBucketObjectLockConfiguration | Unset
        if isinstance(_observed_configuration, Unset):
            observed_configuration = UNSET
        else:
            observed_configuration = ObjectBucketObjectLockConfiguration.from_dict(_observed_configuration)

        _desired_configuration = d.pop("desired_configuration", UNSET)
        desired_configuration: ObjectBucketObjectLockConfiguration | Unset
        if isinstance(_desired_configuration, Unset):
            desired_configuration = UNSET
        else:
            desired_configuration = ObjectBucketObjectLockConfiguration.from_dict(_desired_configuration)

        _last_error_code = d.pop("last_error_code", UNSET)
        last_error_code: ObjectBucketObjectLockLastErrorCode | Unset
        if isinstance(_last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = check_object_bucket_object_lock_last_error_code(_last_error_code)

        object_bucket_object_lock = cls(
            bucket_id=bucket_id,
            state=state,
            revision=revision,
            enabled_required=enabled_required,
            observed_known=observed_known,
            updated_at=updated_at,
            observed_configuration=observed_configuration,
            desired_configuration=desired_configuration,
            last_error_code=last_error_code,
        )

        return object_bucket_object_lock
