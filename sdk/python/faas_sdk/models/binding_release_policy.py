from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_release_policy_mode import BindingReleasePolicyMode, check_binding_release_policy_mode
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_storage_release_requirements import ObjectStorageReleaseRequirements
    from ..models.queue_release_requirements import QueueReleaseRequirements


T = TypeVar("T", bound="BindingReleasePolicy")


@_attrs_define
class BindingReleasePolicy:
    """Stored binding verification requirements and revision for an app deployment scope; unconfigured scopes default to
    off.

    """

    app_id: UUID
    scope: str
    mode: BindingReleasePolicyMode
    revision: int
    max_verification_age: str
    """Canonical Go duration between 1s and 24h; default 10m0s."""
    require_application_ack: bool
    queue_requirements: QueueReleaseRequirements | Unset = UNSET
    """Binding name to explicit required queue evidence. Names match ^[a-z][a-z0-9-]{0,62}$. consumer_readiness
    requires one enabled push consumer with a healthy poll no older than thirty seconds; connectivity coverage
    remains partial."""
    object_storage_requirements: ObjectStorageReleaseRequirements | Unset = UNSET
    """Managed environment prefix to required canary mode in the selected scope. Prefixes match
    ^[A-Z][A-Z0-9_]{0,47}$. read covers listing; read_write requires byte transfer; write and read_write require
    successful cleanup. No implicit probes or writes."""
    updated_at: datetime.datetime | Unset = UNSET
    reason: str | Unset = UNSET
    """Recorded single-line change reason, at most 256 UTF-8 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        mode: str = self.mode

        revision = self.revision

        max_verification_age = self.max_verification_age

        require_application_ack = self.require_application_ack

        queue_requirements: dict[str, Any] | Unset = UNSET
        if not isinstance(self.queue_requirements, Unset):
            queue_requirements = self.queue_requirements.to_dict()

        object_storage_requirements: dict[str, Any] | Unset = UNSET
        if not isinstance(self.object_storage_requirements, Unset):
            object_storage_requirements = self.object_storage_requirements.to_dict()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "mode": mode,
                "revision": revision,
                "max_verification_age": max_verification_age,
                "require_application_ack": require_application_ack,
            }
        )
        if queue_requirements is not UNSET:
            field_dict["queue_requirements"] = queue_requirements
        if object_storage_requirements is not UNSET:
            field_dict["object_storage_requirements"] = object_storage_requirements
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_storage_release_requirements import ObjectStorageReleaseRequirements
        from ..models.queue_release_requirements import QueueReleaseRequirements

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        mode = check_binding_release_policy_mode(d.pop("mode"))

        revision = d.pop("revision")

        max_verification_age = d.pop("max_verification_age")

        require_application_ack = d.pop("require_application_ack")

        _queue_requirements = d.pop("queue_requirements", UNSET)
        queue_requirements: QueueReleaseRequirements | Unset
        if isinstance(_queue_requirements, Unset):
            queue_requirements = UNSET
        else:
            queue_requirements = QueueReleaseRequirements.from_dict(_queue_requirements)

        _object_storage_requirements = d.pop("object_storage_requirements", UNSET)
        object_storage_requirements: ObjectStorageReleaseRequirements | Unset
        if isinstance(_object_storage_requirements, Unset):
            object_storage_requirements = UNSET
        else:
            object_storage_requirements = ObjectStorageReleaseRequirements.from_dict(_object_storage_requirements)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        reason = d.pop("reason", UNSET)

        binding_release_policy = cls(
            app_id=app_id,
            scope=scope,
            mode=mode,
            revision=revision,
            max_verification_age=max_verification_age,
            require_application_ack=require_application_ack,
            queue_requirements=queue_requirements,
            object_storage_requirements=object_storage_requirements,
            updated_at=updated_at,
            reason=reason,
        )

        binding_release_policy.additional_properties = d
        return binding_release_policy

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
