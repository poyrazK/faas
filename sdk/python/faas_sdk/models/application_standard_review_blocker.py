from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_review_blocker_field import (
    ApplicationStandardReviewBlockerField,
    check_application_standard_review_blocker_field,
)
from ..models.application_standard_review_blocker_scope import (
    ApplicationStandardReviewBlockerScope,
    check_application_standard_review_blocker_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardReviewBlocker")


@_attrs_define
class ApplicationStandardReviewBlocker:
    code: str
    app_id: UUID | Unset = UNSET
    scope: ApplicationStandardReviewBlockerScope | Unset = UNSET
    scope_id: UUID | Unset = UNSET
    field: ApplicationStandardReviewBlockerField | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        scope: str | Unset = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        scope_id: str | Unset = UNSET
        if not isinstance(self.scope_id, Unset):
            scope_id = str(self.scope_id)

        field: str | Unset = UNSET
        if not isinstance(self.field, Unset):
            field = self.field

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if scope is not UNSET:
            field_dict["scope"] = scope
        if scope_id is not UNSET:
            field_dict["scope_id"] = scope_id
        if field is not UNSET:
            field_dict["field"] = field

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _scope = d.pop("scope", UNSET)
        scope: ApplicationStandardReviewBlockerScope | Unset
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = check_application_standard_review_blocker_scope(_scope)

        _scope_id = d.pop("scope_id", UNSET)
        scope_id: UUID | Unset
        if isinstance(_scope_id, Unset):
            scope_id = UNSET
        else:
            scope_id = UUID(_scope_id)

        _field = d.pop("field", UNSET)
        field: ApplicationStandardReviewBlockerField | Unset
        if isinstance(_field, Unset):
            field = UNSET
        else:
            field = check_application_standard_review_blocker_field(_field)

        application_standard_review_blocker = cls(
            code=code,
            app_id=app_id,
            scope=scope,
            scope_id=scope_id,
            field=field,
        )

        return application_standard_review_blocker
