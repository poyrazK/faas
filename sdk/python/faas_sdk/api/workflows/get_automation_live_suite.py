from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_live_suite import AutomationLiveSuite
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    suite_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/automations/{name}/live-suites/{suite_id}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
            suite_id=quote(str(suite_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationLiveSuite | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationLiveSuite.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[AutomationLiveSuite | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomationLiveSuite | Problem]:
    """Read current retained suite results in a consistent database snapshot.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveSuite | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AutomationLiveSuite | Problem | None:
    """Read current retained suite results in a consistent database snapshot.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveSuite | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        suite_id=suite_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AutomationLiveSuite | Problem]:
    """Read current retained suite results in a consistent database snapshot.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveSuite | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        suite_id=suite_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    suite_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> AutomationLiveSuite | Problem | None:
    """Read current retained suite results in a consistent database snapshot.

    Args:
        slug (str):
        name (str):
        suite_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveSuite | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            suite_id=suite_id,
            client=client,
        )
    ).parsed
