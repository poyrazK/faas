from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_secret_reference_response import AppSecretReferenceResponse
from ...models.problem import Problem
from ...models.put_app_secret_reference_request import PutAppSecretReferenceRequest
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    key: str,
    *,
    body: PutAppSecretReferenceRequest,
    environment: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/secret-references/{key}".format(
            slug=quote(str(slug), safe=""),
            key=quote(str(key), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppSecretReferenceResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppSecretReferenceResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppSecretReferenceResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretReferenceRequest,
    environment: str,
) -> Response[AppSecretReferenceResponse | Problem]:
    """Select a scoped secret source for a destination environment key.

     Requires secrets write permission and the same MFA posture as sealed secret writes. The named source
    must exist in
    the exact environment and a plaintext variable cannot shadow the destination.
    References share the cross-environment variable quota. The write retains the
    observed catalog identity, invalidates scoped snapshots and applies on a future
    cold wake. Git-owned fields require an active temporary override in enforce mode.

    Args:
        slug (str):
        key (str):
        environment (str):
        body (PutAppSecretReferenceRequest): Named sealed source selected within the exact
            registered project environment; contains no secret value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSecretReferenceResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        body=body,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretReferenceRequest,
    environment: str,
) -> AppSecretReferenceResponse | Problem | None:
    """Select a scoped secret source for a destination environment key.

     Requires secrets write permission and the same MFA posture as sealed secret writes. The named source
    must exist in
    the exact environment and a plaintext variable cannot shadow the destination.
    References share the cross-environment variable quota. The write retains the
    observed catalog identity, invalidates scoped snapshots and applies on a future
    cold wake. Git-owned fields require an active temporary override in enforce mode.

    Args:
        slug (str):
        key (str):
        environment (str):
        body (PutAppSecretReferenceRequest): Named sealed source selected within the exact
            registered project environment; contains no secret value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSecretReferenceResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        key=key,
        client=client,
        body=body,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretReferenceRequest,
    environment: str,
) -> Response[AppSecretReferenceResponse | Problem]:
    """Select a scoped secret source for a destination environment key.

     Requires secrets write permission and the same MFA posture as sealed secret writes. The named source
    must exist in
    the exact environment and a plaintext variable cannot shadow the destination.
    References share the cross-environment variable quota. The write retains the
    observed catalog identity, invalidates scoped snapshots and applies on a future
    cold wake. Git-owned fields require an active temporary override in enforce mode.

    Args:
        slug (str):
        key (str):
        environment (str):
        body (PutAppSecretReferenceRequest): Named sealed source selected within the exact
            registered project environment; contains no secret value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppSecretReferenceResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        key=key,
        body=body,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    key: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutAppSecretReferenceRequest,
    environment: str,
) -> AppSecretReferenceResponse | Problem | None:
    """Select a scoped secret source for a destination environment key.

     Requires secrets write permission and the same MFA posture as sealed secret writes. The named source
    must exist in
    the exact environment and a plaintext variable cannot shadow the destination.
    References share the cross-environment variable quota. The write retains the
    observed catalog identity, invalidates scoped snapshots and applies on a future
    cold wake. Git-owned fields require an active temporary override in enforce mode.

    Args:
        slug (str):
        key (str):
        environment (str):
        body (PutAppSecretReferenceRequest): Named sealed source selected within the exact
            registered project environment; contains no secret value.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppSecretReferenceResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            key=key,
            client=client,
            body=body,
            environment=environment,
        )
    ).parsed
