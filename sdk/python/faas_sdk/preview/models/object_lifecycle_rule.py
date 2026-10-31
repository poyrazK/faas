from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_lifecycle_rule_status import ObjectLifecycleRuleStatus, check_object_lifecycle_rule_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_lifecycle_expiration import ObjectLifecycleExpiration
    from ..models.object_lifecycle_filter import ObjectLifecycleFilter
    from ..models.object_lifecycle_noncurrent_expiration import ObjectLifecycleNoncurrentExpiration


T = TypeVar("T", bound="ObjectLifecycleRule")


@_attrs_define
class ObjectLifecycleRule:
    """At least one action is required. Omitted IDs receive deterministic IDs. Tag filters cannot be combined with
    multipart abort or expired delete marker actions.

    """

    status: ObjectLifecycleRuleStatus
    id: str | Unset = UNSET
    """Unique rule ID; generated when omitted or empty."""
    filter_: ObjectLifecycleFilter | Unset = UNSET
    """Empty filter selects all keys. Prefix and tags are conjunctive; at most ten tags are supported."""
    expiration: ObjectLifecycleExpiration | Unset = UNSET
    """Exactly one action is required. Date must be a UTC midnight. A false marker action is retained as a no-op."""
    noncurrent_version_expiration: ObjectLifecycleNoncurrentExpiration | Unset = UNSET
    """Expire noncurrent versions after their successor age and optional retained-version count both qualify."""
    abort_incomplete_multipart_days: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        id = self.id

        filter_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.filter_, Unset):
            filter_ = self.filter_.to_dict()

        expiration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expiration, Unset):
            expiration = self.expiration.to_dict()

        noncurrent_version_expiration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.noncurrent_version_expiration, Unset):
            noncurrent_version_expiration = self.noncurrent_version_expiration.to_dict()

        abort_incomplete_multipart_days = self.abort_incomplete_multipart_days

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if filter_ is not UNSET:
            field_dict["filter"] = filter_
        if expiration is not UNSET:
            field_dict["expiration"] = expiration
        if noncurrent_version_expiration is not UNSET:
            field_dict["noncurrent_version_expiration"] = noncurrent_version_expiration
        if abort_incomplete_multipart_days is not UNSET:
            field_dict["abort_incomplete_multipart_days"] = abort_incomplete_multipart_days

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_lifecycle_expiration import ObjectLifecycleExpiration
        from ..models.object_lifecycle_filter import ObjectLifecycleFilter
        from ..models.object_lifecycle_noncurrent_expiration import ObjectLifecycleNoncurrentExpiration

        d = dict(src_dict)
        status = check_object_lifecycle_rule_status(d.pop("status"))

        id = d.pop("id", UNSET)

        _filter_ = d.pop("filter", UNSET)
        filter_: ObjectLifecycleFilter | Unset
        if isinstance(_filter_, Unset):
            filter_ = UNSET
        else:
            filter_ = ObjectLifecycleFilter.from_dict(_filter_)

        _expiration = d.pop("expiration", UNSET)
        expiration: ObjectLifecycleExpiration | Unset
        if isinstance(_expiration, Unset):
            expiration = UNSET
        else:
            expiration = ObjectLifecycleExpiration.from_dict(_expiration)

        _noncurrent_version_expiration = d.pop("noncurrent_version_expiration", UNSET)
        noncurrent_version_expiration: ObjectLifecycleNoncurrentExpiration | Unset
        if isinstance(_noncurrent_version_expiration, Unset):
            noncurrent_version_expiration = UNSET
        else:
            noncurrent_version_expiration = ObjectLifecycleNoncurrentExpiration.from_dict(
                _noncurrent_version_expiration
            )

        abort_incomplete_multipart_days = d.pop("abort_incomplete_multipart_days", UNSET)

        object_lifecycle_rule = cls(
            status=status,
            id=id,
            filter_=filter_,
            expiration=expiration,
            noncurrent_version_expiration=noncurrent_version_expiration,
            abort_incomplete_multipart_days=abort_incomplete_multipart_days,
        )

        return object_lifecycle_rule
