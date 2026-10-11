from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dev_patch_status_response import DevPatchStatusResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    project: str,
    generation: int,
    *,
    workspace_id: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["workspace_id"] = workspace_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/dev/sessions/{project}/patches/{generation}".format(
            project=quote(str(project), safe=""),
            generation=quote(str(generation), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DevPatchStatusResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DevPatchStatusResponse.from_dict(response.json())

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
) -> Response[DevPatchStatusResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    project: str,
    generation: int,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> Response[DevPatchStatusResponse | Problem]:
    """Inspect delivery of a developer live patch.

     Reports whether a published developer live patch (ADR-740) reached the running developer
    environment. The state is pending until an instance acknowledges it, then applied, or failed with a
    bounded error code. Never renews the developer lease.

    Args:
        project (str):
        generation (int):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevPatchStatusResponse | Problem]
    """

    kwargs = _get_kwargs(
        project=project,
        generation=generation,
        workspace_id=workspace_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    project: str,
    generation: int,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> DevPatchStatusResponse | Problem | None:
    """Inspect delivery of a developer live patch.

     Reports whether a published developer live patch (ADR-740) reached the running developer
    environment. The state is pending until an instance acknowledges it, then applied, or failed with a
    bounded error code. Never renews the developer lease.

    Args:
        project (str):
        generation (int):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevPatchStatusResponse | Problem
    """

    return sync_detailed(
        project=project,
        generation=generation,
        client=client,
        workspace_id=workspace_id,
    ).parsed


async def asyncio_detailed(
    project: str,
    generation: int,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> Response[DevPatchStatusResponse | Problem]:
    """Inspect delivery of a developer live patch.

     Reports whether a published developer live patch (ADR-740) reached the running developer
    environment. The state is pending until an instance acknowledges it, then applied, or failed with a
    bounded error code. Never renews the developer lease.

    Args:
        project (str):
        generation (int):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevPatchStatusResponse | Problem]
    """

    kwargs = _get_kwargs(
        project=project,
        generation=generation,
        workspace_id=workspace_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    project: str,
    generation: int,
    *,
    client: AuthenticatedClient | Client,
    workspace_id: str | Unset = UNSET,
) -> DevPatchStatusResponse | Problem | None:
    """Inspect delivery of a developer live patch.

     Reports whether a published developer live patch (ADR-740) reached the running developer
    environment. The state is pending until an instance acknowledges it, then applied, or failed with a
    bounded error code. Never renews the developer lease.

    Args:
        project (str):
        generation (int):
        workspace_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevPatchStatusResponse | Problem
    """

    return (
        await asyncio_detailed(
            project=project,
            generation=generation,
            client=client,
            workspace_id=workspace_id,
        )
    ).parsed
