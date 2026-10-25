from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.build_response import BuildResponse


T = TypeVar("T", bound="BuildListResponse")


@_attrs_define
class BuildListResponse:
    """DEPLOY-PROV-6 follow-up / ADR-091 (issue #741 close-out):
    the page shape for GET /v1/builds. Items is one page of
    builds ordered started_at DESC NULLS LAST — queued builds
    (started_at IS NULL) sink to the bottom of the first page
    per the new index builds_deployment_started_idx. The
    next_before cursor is the opaque tuple
    `<rfc3339nano>|<id_hex>` of the LAST row on this page
    (post-review fix; code review surfaced issues 1+2 that
    the id tiebreaker resolves). The started_at segment is
    empty for queued-tail pages (the cursor becomes a pure
    id anchor in the NULL zone). An empty next_before
    signals end-of-list. Round-trip the cursor verbatim on
    subsequent requests; do NOT re-parse or re-encode.

    Mirrors DeploymentListResponse in shape only. The new
    index and the sibling ListBuildsForAccountPaged method
    (state layer) make this O(page-size) regardless of
    account size; the unlimited
    ListBuildsForAccount(ctx, accountID) stays intact for the
    GDPR export at cmd/apid/handlers_account.go.

    """

    items: list[BuildResponse]
    next_before: str | Unset = UNSET
    """Opaque cursor for the next page. Empty = end of list.
    Wire format: `<rfc3339nano>|<id_hex>` (pipe-separated).
    The id is the Build.ID of the LAST row on this page;
    the started_at segment is RFC3339Nano (sub-second
    preserved) for non-queued rows, or empty for queued
    rows (the cursor then keys on id alone in the NULL
    zone). Round-trip verbatim — server-parsed, no client
    re-encoding.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.build_response import BuildResponse

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = BuildResponse.from_dict(items_item_data)

            items.append(items_item)

        next_before = d.pop("next_before", UNSET)

        build_list_response = cls(
            items=items,
            next_before=next_before,
        )

        build_list_response.additional_properties = d
        return build_list_response

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
