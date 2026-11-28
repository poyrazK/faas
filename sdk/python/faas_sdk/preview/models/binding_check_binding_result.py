from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_check_binding_result_object_storage_mode import (
    BindingCheckBindingResultObjectStorageMode,
    check_binding_check_binding_result_object_storage_mode,
)
from ..models.binding_check_binding_result_required_object_storage_mode import (
    BindingCheckBindingResultRequiredObjectStorageMode,
    check_binding_check_binding_result_required_object_storage_mode,
)
from ..models.binding_check_binding_result_required_queue_evidence import (
    BindingCheckBindingResultRequiredQueueEvidence,
    check_binding_check_binding_result_required_queue_evidence,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_application_adoption import BindingApplicationAdoption


T = TypeVar("T", bound="BindingCheckBindingResult")


@_attrs_define
class BindingCheckBindingResult:
    """Preflight status and safe evidence summary for one binding."""

    type_: str
    name: str
    scope: str
    status: str
    """passed, blocked, unsupported or skipped."""
    required_queue_evidence: BindingCheckBindingResultRequiredQueueEvidence | Unset = UNSET
    """Explicit scheduler readiness requirement; a passed policy result does not imply queue connectivity."""
    object_storage_mode: BindingCheckBindingResultObjectStorageMode | Unset = UNSET
    """Exact admitted canary mode. Older evidence with no mode is listing-only and cannot satisfy write
    requirements."""
    required_object_storage_mode: BindingCheckBindingResultRequiredObjectStorageMode | Unset = UNSET
    binding: str | Unset = UNSET
    reason: str | Unset = UNSET
    verification_status: str | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    refresh_status: str | Unset = UNSET
    application_adoption: BindingApplicationAdoption | Unset = UNSET
    """Metadata-only application self-attestations for managed PostgreSQL or object-storage secrets. Counts refer
    to workload/secret pairs. Reads use a single state snapshot and include missing reports. Task guests, jobs,
    mirrors and unauthorized workloads are excluded. Receipts expire when secret versions change, independently of
    probe age. An older guest projection does not erase a newer application receipt. complete describes this
    optional observation read; a read failure leaves default checks unchanged and blocks strict checks."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        name = self.name

        scope = self.scope

        status = self.status

        required_queue_evidence: str | Unset = UNSET
        if not isinstance(self.required_queue_evidence, Unset):
            required_queue_evidence = self.required_queue_evidence

        object_storage_mode: str | Unset = UNSET
        if not isinstance(self.object_storage_mode, Unset):
            object_storage_mode = self.object_storage_mode

        required_object_storage_mode: str | Unset = UNSET
        if not isinstance(self.required_object_storage_mode, Unset):
            required_object_storage_mode = self.required_object_storage_mode

        binding = self.binding

        reason = self.reason

        verification_status = self.verification_status

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        refresh_status = self.refresh_status

        application_adoption: dict[str, Any] | Unset = UNSET
        if not isinstance(self.application_adoption, Unset):
            application_adoption = self.application_adoption.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "name": name,
                "scope": scope,
                "status": status,
            }
        )
        if required_queue_evidence is not UNSET:
            field_dict["required_queue_evidence"] = required_queue_evidence
        if object_storage_mode is not UNSET:
            field_dict["object_storage_mode"] = object_storage_mode
        if required_object_storage_mode is not UNSET:
            field_dict["required_object_storage_mode"] = required_object_storage_mode
        if binding is not UNSET:
            field_dict["binding"] = binding
        if reason is not UNSET:
            field_dict["reason"] = reason
        if verification_status is not UNSET:
            field_dict["verification_status"] = verification_status
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if refresh_status is not UNSET:
            field_dict["refresh_status"] = refresh_status
        if application_adoption is not UNSET:
            field_dict["application_adoption"] = application_adoption

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_application_adoption import BindingApplicationAdoption

        d = dict(src_dict)
        type_ = d.pop("type")

        name = d.pop("name")

        scope = d.pop("scope")

        status = d.pop("status")

        _required_queue_evidence = d.pop("required_queue_evidence", UNSET)
        required_queue_evidence: BindingCheckBindingResultRequiredQueueEvidence | Unset
        if isinstance(_required_queue_evidence, Unset):
            required_queue_evidence = UNSET
        else:
            required_queue_evidence = check_binding_check_binding_result_required_queue_evidence(
                _required_queue_evidence
            )

        _object_storage_mode = d.pop("object_storage_mode", UNSET)
        object_storage_mode: BindingCheckBindingResultObjectStorageMode | Unset
        if isinstance(_object_storage_mode, Unset):
            object_storage_mode = UNSET
        else:
            object_storage_mode = check_binding_check_binding_result_object_storage_mode(_object_storage_mode)

        _required_object_storage_mode = d.pop("required_object_storage_mode", UNSET)
        required_object_storage_mode: BindingCheckBindingResultRequiredObjectStorageMode | Unset
        if isinstance(_required_object_storage_mode, Unset):
            required_object_storage_mode = UNSET
        else:
            required_object_storage_mode = check_binding_check_binding_result_required_object_storage_mode(
                _required_object_storage_mode
            )

        binding = d.pop("binding", UNSET)

        reason = d.pop("reason", UNSET)

        verification_status = d.pop("verification_status", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        refresh_status = d.pop("refresh_status", UNSET)

        _application_adoption = d.pop("application_adoption", UNSET)
        application_adoption: BindingApplicationAdoption | Unset
        if isinstance(_application_adoption, Unset):
            application_adoption = UNSET
        else:
            application_adoption = BindingApplicationAdoption.from_dict(_application_adoption)

        binding_check_binding_result = cls(
            type_=type_,
            name=name,
            scope=scope,
            status=status,
            required_queue_evidence=required_queue_evidence,
            object_storage_mode=object_storage_mode,
            required_object_storage_mode=required_object_storage_mode,
            binding=binding,
            reason=reason,
            verification_status=verification_status,
            checked_at=checked_at,
            refresh_status=refresh_status,
            application_adoption=application_adoption,
        )

        binding_check_binding_result.additional_properties = d
        return binding_check_binding_result

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
