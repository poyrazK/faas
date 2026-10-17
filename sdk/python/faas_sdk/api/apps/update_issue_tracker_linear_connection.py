from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_tracker_linear_connection import IssueTrackerLinearConnection
from ...models.problem import Problem
from ...models.update_issue_tracker_linear_connection_request import UpdateIssueTrackerLinearConnectionRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: UpdateIssueTrackerLinearConnectionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/issue-tracker-linear-connection".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | IssueTrackerLinearConnection | Problem | None:
    if response.status_code == 200:
        response_200 = IssueTrackerLinearConnection.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | IssueTrackerLinearConnection | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerLinearConnectionRequest,
) -> Response[Any | IssueTrackerLinearConnection | Problem]:
    """Connect or disconnect an owner-scoped Linear personal API key.

     Requires the app owner with deploy-write and session MFA. Connecting additionally requires Issues
    entitlement and an account eligible to deploy. Verifies workspace and viewer and active team before
    sealing credentials. expected_revision fences competing changes. Disconnect erases credentials
    without provider access and remains available after a plan downgrade. This does not enable automatic
    sync or perform provider writes.

    Args:
        slug (str):
        body (UpdateIssueTrackerLinearConnectionRequest): Explicit connection or disconnection
            against a known revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueTrackerLinearConnection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerLinearConnectionRequest,
) -> Any | IssueTrackerLinearConnection | Problem | None:
    """Connect or disconnect an owner-scoped Linear personal API key.

     Requires the app owner with deploy-write and session MFA. Connecting additionally requires Issues
    entitlement and an account eligible to deploy. Verifies workspace and viewer and active team before
    sealing credentials. expected_revision fences competing changes. Disconnect erases credentials
    without provider access and remains available after a plan downgrade. This does not enable automatic
    sync or perform provider writes.

    Args:
        slug (str):
        body (UpdateIssueTrackerLinearConnectionRequest): Explicit connection or disconnection
            against a known revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueTrackerLinearConnection | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerLinearConnectionRequest,
) -> Response[Any | IssueTrackerLinearConnection | Problem]:
    """Connect or disconnect an owner-scoped Linear personal API key.

     Requires the app owner with deploy-write and session MFA. Connecting additionally requires Issues
    entitlement and an account eligible to deploy. Verifies workspace and viewer and active team before
    sealing credentials. expected_revision fences competing changes. Disconnect erases credentials
    without provider access and remains available after a plan downgrade. This does not enable automatic
    sync or perform provider writes.

    Args:
        slug (str):
        body (UpdateIssueTrackerLinearConnectionRequest): Explicit connection or disconnection
            against a known revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueTrackerLinearConnection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: UpdateIssueTrackerLinearConnectionRequest,
) -> Any | IssueTrackerLinearConnection | Problem | None:
    """Connect or disconnect an owner-scoped Linear personal API key.

     Requires the app owner with deploy-write and session MFA. Connecting additionally requires Issues
    entitlement and an account eligible to deploy. Verifies workspace and viewer and active team before
    sealing credentials. expected_revision fences competing changes. Disconnect erases credentials
    without provider access and remains available after a plan downgrade. This does not enable automatic
    sync or perform provider writes.

    Args:
        slug (str):
        body (UpdateIssueTrackerLinearConnectionRequest): Explicit connection or disconnection
            against a known revision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueTrackerLinearConnection | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
