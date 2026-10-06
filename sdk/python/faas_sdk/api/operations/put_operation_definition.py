from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_definition_response import OperationDefinitionResponse
from ...models.operation_definition_spec import OperationDefinitionSpec
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment_id: UUID,
    name: str,
    *,
    body: OperationDefinitionSpec,
    x_gregale_release: UUID | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(x_gregale_release, Unset):
        headers["X-Gregale-Release"] = x_gregale_release

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/deployments/{deployment_id}/operation-definitions/{name}".format(
            slug=quote(str(slug), safe=""),
            deployment_id=quote(str(deployment_id), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OperationDefinitionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OperationDefinitionResponse.from_dict(response.json())

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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
) -> Response[OperationDefinitionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment_id: UUID,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationDefinitionSpec,
    x_gregale_release: UUID | Unset = UNSET,
) -> Response[OperationDefinitionResponse | Problem]:
    """Install an immutable operation definition.

     Requires deploy:write. Normal source deployment resolves schemas and installs definitions
    automatically. Repeating the same resolved contract returns the same definition. Changing a
    definition on the same deployment conflicts. This staged API has no production admission switch;
    definition registration and submission return 503 until the HTTP execution adapter is qualified.

    Args:
        slug (str):
        deployment_id (UUID):
        name (str):
        x_gregale_release (UUID | Unset):
        body (OperationDefinitionSpec): Resolved immutable contract for one HTTP handler.
            Ownership comes from verified authentication, never input fields. Production admission
            stays disabled until the HTTP execution adapter is qualified.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationDefinitionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        name=name,
        body=body,
        x_gregale_release=x_gregale_release,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment_id: UUID,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationDefinitionSpec,
    x_gregale_release: UUID | Unset = UNSET,
) -> OperationDefinitionResponse | Problem | None:
    """Install an immutable operation definition.

     Requires deploy:write. Normal source deployment resolves schemas and installs definitions
    automatically. Repeating the same resolved contract returns the same definition. Changing a
    definition on the same deployment conflicts. This staged API has no production admission switch;
    definition registration and submission return 503 until the HTTP execution adapter is qualified.

    Args:
        slug (str):
        deployment_id (UUID):
        name (str):
        x_gregale_release (UUID | Unset):
        body (OperationDefinitionSpec): Resolved immutable contract for one HTTP handler.
            Ownership comes from verified authentication, never input fields. Production admission
            stays disabled until the HTTP execution adapter is qualified.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationDefinitionResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment_id=deployment_id,
        name=name,
        client=client,
        body=body,
        x_gregale_release=x_gregale_release,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment_id: UUID,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationDefinitionSpec,
    x_gregale_release: UUID | Unset = UNSET,
) -> Response[OperationDefinitionResponse | Problem]:
    """Install an immutable operation definition.

     Requires deploy:write. Normal source deployment resolves schemas and installs definitions
    automatically. Repeating the same resolved contract returns the same definition. Changing a
    definition on the same deployment conflicts. This staged API has no production admission switch;
    definition registration and submission return 503 until the HTTP execution adapter is qualified.

    Args:
        slug (str):
        deployment_id (UUID):
        name (str):
        x_gregale_release (UUID | Unset):
        body (OperationDefinitionSpec): Resolved immutable contract for one HTTP handler.
            Ownership comes from verified authentication, never input fields. Production admission
            stays disabled until the HTTP execution adapter is qualified.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OperationDefinitionResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment_id=deployment_id,
        name=name,
        body=body,
        x_gregale_release=x_gregale_release,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment_id: UUID,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: OperationDefinitionSpec,
    x_gregale_release: UUID | Unset = UNSET,
) -> OperationDefinitionResponse | Problem | None:
    """Install an immutable operation definition.

     Requires deploy:write. Normal source deployment resolves schemas and installs definitions
    automatically. Repeating the same resolved contract returns the same definition. Changing a
    definition on the same deployment conflicts. This staged API has no production admission switch;
    definition registration and submission return 503 until the HTTP execution adapter is qualified.

    Args:
        slug (str):
        deployment_id (UUID):
        name (str):
        x_gregale_release (UUID | Unset):
        body (OperationDefinitionSpec): Resolved immutable contract for one HTTP handler.
            Ownership comes from verified authentication, never input fields. Production admission
            stays disabled until the HTTP execution adapter is qualified.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OperationDefinitionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment_id=deployment_id,
            name=name,
            client=client,
            body=body,
            x_gregale_release=x_gregale_release,
        )
    ).parsed
