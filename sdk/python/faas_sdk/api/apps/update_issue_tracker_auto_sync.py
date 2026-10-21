from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_tracker_auto_sync import IssueTrackerAutoSync
from ...models.problem import Problem
from ...models.update_issue_tracker_auto_sync_request import UpdateIssueTrackerAutoSyncRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    body: UpdateIssueTrackerAutoSyncRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/issues/{issue_id}/tracker-links/{link_id}/auto-sync".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
            link_id=quote(str(link_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | IssueTrackerAutoSync | Problem | None:
    if response.status_code == 200:
        response_200 = IssueTrackerAutoSync.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 502:
        response_502 = cast(Any, None)
        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | IssueTrackerAutoSync | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerAutoSyncRequest,
) -> Response[Any | IssueTrackerAutoSync | Problem]:
    """Enable or disable future Gregale-to-tracker lifecycle pushes.

     Requires the app owner, Issues entitlement, deploy-write and session MFA. expected_revision fences
    competing policy changes. Enabling requires either expected_status for GitHub or
    expected_linear_status plus distinct linear_open_state_id and linear_resolved_state_id for Linear,
    and expected_issue_state; the lifecycles must already match. Verifies active Linear workflows in the
    connected team. Pins the canonical group and owned repository binding or Linear connection revision.
    The background worker preserves uncertain pending writes and pauses on provider edits or unsupported
    states or changed groups/connections. Disabling does not require live provider access. Linear
    pending writes with an unconfirmed outcome are observed without redispatch; explicitly align and re-
    enable to resume. A provider write already dispatched during disable cannot be recalled.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):
        body (UpdateIssueTrackerAutoSyncRequest): Explicit direction delegation against a known
            policy revision and aligned native/source lifecycle snapshot. Inbound closures are held
            for fixing-deployment review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueTrackerAutoSync | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        link_id=link_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerAutoSyncRequest,
) -> Any | IssueTrackerAutoSync | Problem | None:
    """Enable or disable future Gregale-to-tracker lifecycle pushes.

     Requires the app owner, Issues entitlement, deploy-write and session MFA. expected_revision fences
    competing policy changes. Enabling requires either expected_status for GitHub or
    expected_linear_status plus distinct linear_open_state_id and linear_resolved_state_id for Linear,
    and expected_issue_state; the lifecycles must already match. Verifies active Linear workflows in the
    connected team. Pins the canonical group and owned repository binding or Linear connection revision.
    The background worker preserves uncertain pending writes and pauses on provider edits or unsupported
    states or changed groups/connections. Disabling does not require live provider access. Linear
    pending writes with an unconfirmed outcome are observed without redispatch; explicitly align and re-
    enable to resume. A provider write already dispatched during disable cannot be recalled.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):
        body (UpdateIssueTrackerAutoSyncRequest): Explicit direction delegation against a known
            policy revision and aligned native/source lifecycle snapshot. Inbound closures are held
            for fixing-deployment review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueTrackerAutoSync | Problem
    """

    return sync_detailed(
        slug=slug,
        issue_id=issue_id,
        link_id=link_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerAutoSyncRequest,
) -> Response[Any | IssueTrackerAutoSync | Problem]:
    """Enable or disable future Gregale-to-tracker lifecycle pushes.

     Requires the app owner, Issues entitlement, deploy-write and session MFA. expected_revision fences
    competing policy changes. Enabling requires either expected_status for GitHub or
    expected_linear_status plus distinct linear_open_state_id and linear_resolved_state_id for Linear,
    and expected_issue_state; the lifecycles must already match. Verifies active Linear workflows in the
    connected team. Pins the canonical group and owned repository binding or Linear connection revision.
    The background worker preserves uncertain pending writes and pauses on provider edits or unsupported
    states or changed groups/connections. Disabling does not require live provider access. Linear
    pending writes with an unconfirmed outcome are observed without redispatch; explicitly align and re-
    enable to resume. A provider write already dispatched during disable cannot be recalled.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):
        body (UpdateIssueTrackerAutoSyncRequest): Explicit direction delegation against a known
            policy revision and aligned native/source lifecycle snapshot. Inbound closures are held
            for fixing-deployment review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueTrackerAutoSync | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        link_id=link_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    issue_id: UUID,
    link_id: UUID,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerAutoSyncRequest,
) -> Any | IssueTrackerAutoSync | Problem | None:
    """Enable or disable future Gregale-to-tracker lifecycle pushes.

     Requires the app owner, Issues entitlement, deploy-write and session MFA. expected_revision fences
    competing policy changes. Enabling requires either expected_status for GitHub or
    expected_linear_status plus distinct linear_open_state_id and linear_resolved_state_id for Linear,
    and expected_issue_state; the lifecycles must already match. Verifies active Linear workflows in the
    connected team. Pins the canonical group and owned repository binding or Linear connection revision.
    The background worker preserves uncertain pending writes and pauses on provider edits or unsupported
    states or changed groups/connections. Disabling does not require live provider access. Linear
    pending writes with an unconfirmed outcome are observed without redispatch; explicitly align and re-
    enable to resume. A provider write already dispatched during disable cannot be recalled.

    Args:
        slug (str):
        issue_id (UUID):
        link_id (UUID):
        body (UpdateIssueTrackerAutoSyncRequest): Explicit direction delegation against a known
            policy revision and aligned native/source lifecycle snapshot. Inbound closures are held
            for fixing-deployment review.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueTrackerAutoSync | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            link_id=link_id,
            client=client,
            body=body,
        )
    ).parsed
