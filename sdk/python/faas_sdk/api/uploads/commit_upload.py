from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    deployment_attempt_id: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(deployment_attempt_id, Unset):
        headers["Deployment-Attempt-ID"] = deployment_attempt_id

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/uploads/{id}/commit".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
    if response.status_code == 201:
        response_201 = DeploymentResponse.from_dict(response.json())

        return response_201

    if response.status_code == 202:
        response_202 = DeploymentResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DeploymentResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_attempt_id: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Finalize the session, validate the tarball, enqueue the build.

    Args:
        id (str):
        deployment_attempt_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        deployment_attempt_id=deployment_attempt_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_attempt_id: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Finalize the session, validate the tarball, enqueue the build.

    Args:
        id (str):
        deployment_attempt_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        deployment_attempt_id=deployment_attempt_id,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_attempt_id: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Finalize the session, validate the tarball, enqueue the build.

    Args:
        id (str):
        deployment_attempt_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        deployment_attempt_id=deployment_attempt_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_attempt_id: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Finalize the session, validate the tarball, enqueue the build.

    Args:
        id (str):
        deployment_attempt_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            deployment_attempt_id=deployment_attempt_id,
        )
    ).parsed
