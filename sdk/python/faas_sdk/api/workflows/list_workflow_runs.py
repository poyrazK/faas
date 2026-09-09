from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_workflow_runs_response import ListWorkflowRunsResponse
from ...models.list_workflow_runs_status import ListWorkflowRunsStatus
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    status: ListWorkflowRunsStatus | Unset = UNSET,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["limit"] = limit

    params["offset"] = offset

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/workflows/runs".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListWorkflowRunsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListWorkflowRunsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[ListWorkflowRunsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    status: ListWorkflowRunsStatus | Unset = UNSET,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> Response[ListWorkflowRunsResponse | Problem]:
    """List durable workflow runs for an app.

    Args:
        slug (str):
        status (ListWorkflowRunsStatus | Unset):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListWorkflowRunsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        status=status,
        limit=limit,
        offset=offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    status: ListWorkflowRunsStatus | Unset = UNSET,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> ListWorkflowRunsResponse | Problem | None:
    """List durable workflow runs for an app.

    Args:
        slug (str):
        status (ListWorkflowRunsStatus | Unset):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListWorkflowRunsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        status=status,
        limit=limit,
        offset=offset,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    status: ListWorkflowRunsStatus | Unset = UNSET,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> Response[ListWorkflowRunsResponse | Problem]:
    """List durable workflow runs for an app.

    Args:
        slug (str):
        status (ListWorkflowRunsStatus | Unset):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListWorkflowRunsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        status=status,
        limit=limit,
        offset=offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    status: ListWorkflowRunsStatus | Unset = UNSET,
    limit: int | Unset = 50,
    offset: int | Unset = 0,
) -> ListWorkflowRunsResponse | Problem | None:
    """List durable workflow runs for an app.

    Args:
        slug (str):
        status (ListWorkflowRunsStatus | Unset):
        limit (int | Unset):  Default: 50.
        offset (int | Unset):  Default: 0.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListWorkflowRunsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            status=status,
            limit=limit,
            offset=offset,
        )
    ).parsed
