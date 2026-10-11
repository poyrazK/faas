from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dev_session_response import DevSessionResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    project: str,
    *,
    workspace_id: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["workspace_id"] = workspace_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/dev/sessions/{project}".format(
            project=quote(str(project), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DevSessionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DevSessionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DevSessionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> Response[DevSessionResponse | Problem]:
    """Inspect a remote developer environment.

     Returns the stable URL, app, lease expiry, and safe PostgreSQL binding state of one developer
    environment. Unlike the upsert, this read never renews the lease or provisions resources.

    Args:
        project (str):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevSessionResponse | Problem]
    """

    kwargs = _get_kwargs(
        project=project,
        workspace_id=workspace_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> DevSessionResponse | Problem | None:
    """Inspect a remote developer environment.

     Returns the stable URL, app, lease expiry, and safe PostgreSQL binding state of one developer
    environment. Unlike the upsert, this read never renews the lease or provisions resources.

    Args:
        project (str):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevSessionResponse | Problem
    """

    return sync_detailed(
        project=project,
        client=client,
        workspace_id=workspace_id,
    ).parsed


async def asyncio_detailed(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> Response[DevSessionResponse | Problem]:
    """Inspect a remote developer environment.

     Returns the stable URL, app, lease expiry, and safe PostgreSQL binding state of one developer
    environment. Unlike the upsert, this read never renews the lease or provisions resources.

    Args:
        project (str):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevSessionResponse | Problem]
    """

    kwargs = _get_kwargs(
        project=project,
        workspace_id=workspace_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> DevSessionResponse | Problem | None:
    """Inspect a remote developer environment.

     Returns the stable URL, app, lease expiry, and safe PostgreSQL binding state of one developer
    environment. Unlike the upsert, this read never renews the lease or provisions resources.

    Args:
        project (str):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevSessionResponse | Problem
    """

    return (
        await asyncio_detailed(
            project=project,
            client=client,
            workspace_id=workspace_id,
        )
    ).parsed
