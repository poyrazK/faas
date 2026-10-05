from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.application_standard_source_mode import (
    ApplicationStandardSourceMode,
    check_application_standard_source_mode,
)
from ..models.application_standard_source_override import (
    ApplicationStandardSourceOverride,
    check_application_standard_source_override,
)
from ..models.application_standard_source_scope import (
    ApplicationStandardSourceScope,
    check_application_standard_source_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardSource")


@_attrs_define
class ApplicationStandardSource:
    """Inherited assignment, enforcement mode and any applicable exception contributing to one field."""

    standard_id: UUID
    version: int
    scope: ApplicationStandardSourceScope
    mode: ApplicationStandardSourceMode
    override: ApplicationStandardSourceOverride
    assignment_id: UUID | Unset = UNSET
    scope_id: UUID | Unset = UNSET
    exception_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        standard_id = str(self.standard_id)

        version = self.version

        scope: str = self.scope

        mode: str = self.mode

        override: str = self.override

        assignment_id: str | Unset = UNSET
        if not isinstance(self.assignment_id, Unset):
            assignment_id = str(self.assignment_id)

        scope_id: str | Unset = UNSET
        if not isinstance(self.scope_id, Unset):
            scope_id = str(self.scope_id)

        exception_id: str | Unset = UNSET
        if not isinstance(self.exception_id, Unset):
            exception_id = str(self.exception_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "standard_id": standard_id,
                "version": version,
                "scope": scope,
                "mode": mode,
                "override": override,
            }
        )
        if assignment_id is not UNSET:
            field_dict["assignment_id"] = assignment_id
        if scope_id is not UNSET:
            field_dict["scope_id"] = scope_id
        if exception_id is not UNSET:
            field_dict["exception_id"] = exception_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        standard_id = UUID(d.pop("standard_id"))

        version = d.pop("version")

        scope = check_application_standard_source_scope(d.pop("scope"))

        mode = check_application_standard_source_mode(d.pop("mode"))

        override = check_application_standard_source_override(d.pop("override"))

        _assignment_id = d.pop("assignment_id", UNSET)
        assignment_id: UUID | Unset
        if isinstance(_assignment_id, Unset):
            assignment_id = UNSET
        else:
            assignment_id = UUID(_assignment_id)

        _scope_id = d.pop("scope_id", UNSET)
        scope_id: UUID | Unset
        if isinstance(_scope_id, Unset):
            scope_id = UNSET
        else:
            scope_id = UUID(_scope_id)

        _exception_id = d.pop("exception_id", UNSET)
        exception_id: UUID | Unset
        if isinstance(_exception_id, Unset):
            exception_id = UNSET
        else:
            exception_id = UUID(_exception_id)

        application_standard_source = cls(
            standard_id=standard_id,
            version=version,
            scope=scope,
            mode=mode,
            override=override,
            assignment_id=assignment_id,
            scope_id=scope_id,
            exception_id=exception_id,
        )

        application_standard_source.additional_properties = d
        return application_standard_source

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
