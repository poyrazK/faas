from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_issues_response import ListIssuesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    state: str | Unset = UNSET,
    environment: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["state"] = state

    params["environment"] = environment

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/issues".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListIssuesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListIssuesResponse.from_dict(response.json())

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
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[ListIssuesResponse | Problem]:
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
    state: str | Unset = UNSET,
    environment: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> Response[ListIssuesResponse | Problem]:
    """List durable grouped application issues.

    Args:
        slug (str):
        state (str | Unset):
        environment (str | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListIssuesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        state=state,
        environment=environment,
        cursor=cursor,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: str | Unset = UNSET,
    environment: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> ListIssuesResponse | Problem | None:
    """List durable grouped application issues.

    Args:
        slug (str):
        state (str | Unset):
        environment (str | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListIssuesResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        state=state,
        environment=environment,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: str | Unset = UNSET,
    environment: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> Response[ListIssuesResponse | Problem]:
    """List durable grouped application issues.

    Args:
        slug (str):
        state (str | Unset):
        environment (str | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListIssuesResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        state=state,
        environment=environment,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    state: str | Unset = UNSET,
    environment: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
) -> ListIssuesResponse | Problem | None:
    """List durable grouped application issues.

    Args:
        slug (str):
        state (str | Unset):
        environment (str | Unset):
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListIssuesResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            state=state,
            environment=environment,
            cursor=cursor,
        )
    ).parsed
