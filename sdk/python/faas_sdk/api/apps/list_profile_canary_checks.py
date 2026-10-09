from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.profile_canary_history_page import ProfileCanaryHistoryPage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment: UUID,
    *,
    limit: int | Unset = 5,
    before: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["before"] = before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/profiles/canary-checks/{deployment}".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileCanaryHistoryPage | None:
    if response.status_code == 200:
        response_200 = ProfileCanaryHistoryPage.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProfileCanaryHistoryPage]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: str | Unset = UNSET,
) -> Response[Problem | ProfileCanaryHistoryPage]:
    """List retained CPU assessments for a deployment's canary stages.

     Requires app-read access and completed MFA. Returns bounded, newest-first stage assessments retained
    for 30 days, including exact fixed profile windows, policy revision, threshold evidence and retry
    state. Use next_cursor as before to read older stages. The read never queries profile samples.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileCanaryHistoryPage]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: str | Unset = UNSET,
) -> Problem | ProfileCanaryHistoryPage | None:
    """List retained CPU assessments for a deployment's canary stages.

     Requires app-read access and completed MFA. Returns bounded, newest-first stage assessments retained
    for 30 days, including exact fixed profile windows, policy revision, threshold evidence and retry
    state. Use next_cursor as before to read older stages. The read never queries profile samples.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileCanaryHistoryPage
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: str | Unset = UNSET,
) -> Response[Problem | ProfileCanaryHistoryPage]:
    """List retained CPU assessments for a deployment's canary stages.

     Requires app-read access and completed MFA. Returns bounded, newest-first stage assessments retained
    for 30 days, including exact fixed profile windows, policy revision, threshold evidence and retry
    state. Use next_cursor as before to read older stages. The read never queries profile samples.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileCanaryHistoryPage]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 5,
    before: str | Unset = UNSET,
) -> Problem | ProfileCanaryHistoryPage | None:
    """List retained CPU assessments for a deployment's canary stages.

     Requires app-read access and completed MFA. Returns bounded, newest-first stage assessments retained
    for 30 days, including exact fixed profile windows, policy revision, threshold evidence and retry
    state. Use next_cursor as before to read older stages. The read never queries profile samples.

    Args:
        slug (str):
        deployment (UUID):
        limit (int | Unset):  Default: 5.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileCanaryHistoryPage
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
