from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.put_global_image_release_request_regional_api_keys import PutGlobalImageReleaseRequestRegionalApiKeys


T = TypeVar("T", bound="PutGlobalImageReleaseRequest")


@_attrs_define
class PutGlobalImageReleaseRequest:
    """Global image release with sealed scoped consent and background recovery. Regional admin keys are never stored or
    returned.

    """

    image: str
    regional_api_keys: PutGlobalImageReleaseRequestRegionalApiKeys | Unset = _attrs_field(default=UNSET, repr=False)
    """Request-only regional account-admin keys indexed by region, supplied to establish or renew scoped consent.
    Active consent supports recovery and later releases without these keys."""

    def to_dict(self) -> dict[str, Any]:
        image = self.image

        regional_api_keys: dict[str, Any] | Unset = UNSET
        if not isinstance(self.regional_api_keys, Unset):
            regional_api_keys = self.regional_api_keys.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "image": image,
            }
        )
        if regional_api_keys is not UNSET:
            field_dict["regional_api_keys"] = regional_api_keys

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.put_global_image_release_request_regional_api_keys import (
            PutGlobalImageReleaseRequestRegionalApiKeys,
        )

        d = dict(src_dict)
        image = d.pop("image")

        _regional_api_keys = d.pop("regional_api_keys", UNSET)
        regional_api_keys: PutGlobalImageReleaseRequestRegionalApiKeys | Unset
        if isinstance(_regional_api_keys, Unset):
            regional_api_keys = UNSET
        else:
            regional_api_keys = PutGlobalImageReleaseRequestRegionalApiKeys.from_dict(_regional_api_keys)

        put_global_image_release_request = cls(
            image=image,
            regional_api_keys=regional_api_keys,
        )

        return put_global_image_release_request
