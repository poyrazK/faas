from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_environment_shared_secret_response import ProjectEnvironmentSharedSecretResponse
from ...models.set_project_environment_shared_secret_request import SetProjectEnvironmentSharedSecretRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    environment: str,
    key: str,
    *,
    body: SetProjectEnvironmentSharedSecretRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/projects/{slug}/environments/{environment}/secrets/{key}".format(
            slug=quote(str(slug), safe=""),
            environment=quote(str(environment), safe=""),
            key=quote(str(key), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectEnvironmentSharedSecretResponse | None:
    if response.status_code == 200:
        response_200 = ProjectEnvironmentSharedSecretResponse.from_dict(response.json())

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ProjectEnvironmentSharedSecretResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetProjectEnvironmentSharedSecretRequest,
) -> Response[Problem | ProjectEnvironmentSharedSecretResponse]:
    """Set a secret shared by every service of a project environment.

     Seals the value for the fleet and stores it once for the environment
    (ADR-1139). Each service receives it at its next boot unless it sets
    the same key itself; services' snapshots in the environment are
    invalidated. Platform identity keys are rejected.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (SetProjectEnvironmentSharedSecretRequest): The plaintext value to seal. Never
            returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentSharedSecretResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetProjectEnvironmentSharedSecretRequest,
) -> Problem | ProjectEnvironmentSharedSecretResponse | None:
    """Set a secret shared by every service of a project environment.

     Seals the value for the fleet and stores it once for the environment
    (ADR-1139). Each service receives it at its next boot unless it sets
    the same key itself; services' snapshots in the environment are
    invalidated. Platform identity keys are rejected.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (SetProjectEnvironmentSharedSecretRequest): The plaintext value to seal. Never
            returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentSharedSecretResponse
    """

    return sync_detailed(
        slug=slug,
        environment=environment,
        key=key,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetProjectEnvironmentSharedSecretRequest,
) -> Response[Problem | ProjectEnvironmentSharedSecretResponse]:
    """Set a secret shared by every service of a project environment.

     Seals the value for the fleet and stores it once for the environment
    (ADR-1139). Each service receives it at its next boot unless it sets
    the same key itself; services' snapshots in the environment are
    invalidated. Platform identity keys are rejected.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (SetProjectEnvironmentSharedSecretRequest): The plaintext value to seal. Never
            returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectEnvironmentSharedSecretResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        environment=environment,
        key=key,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    environment: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetProjectEnvironmentSharedSecretRequest,
) -> Problem | ProjectEnvironmentSharedSecretResponse | None:
    """Set a secret shared by every service of a project environment.

     Seals the value for the fleet and stores it once for the environment
    (ADR-1139). Each service receives it at its next boot unless it sets
    the same key itself; services' snapshots in the environment are
    invalidated. Platform identity keys are rejected.

    Args:
        slug (str):
        environment (str):
        key (str):
        body (SetProjectEnvironmentSharedSecretRequest): The plaintext value to seal. Never
            returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectEnvironmentSharedSecretResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            environment=environment,
            key=key,
            client=client,
            body=body,
        )
    ).parsed
