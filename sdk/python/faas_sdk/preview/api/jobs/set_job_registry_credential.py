from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.job_registry_credential_response import JobRegistryCredentialResponse
from ...models.problem import Problem
from ...models.put_job_registry_credential_request import PutJobRegistryCredentialRequest
from ...types import Response


def _get_kwargs(
    name: str,
    *,
    body: PutJobRegistryCredentialRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/jobs/{name}/registry-credentials".format(
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> JobRegistryCredentialResponse | Problem | None:
    if response.status_code == 200:
        response_200 = JobRegistryCredentialResponse.from_dict(response.json())

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
) -> Response[JobRegistryCredentialResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutJobRegistryCredentialRequest,
) -> Response[JobRegistryCredentialResponse | Problem]:
    """Set or replace a sealed private-registry credential for a job.

     Seals the plaintext password under namespace `registry_creds` and
    stores it against the job and registry host. Replacements do not
    consume another per-job quota slot.

    Args:
        name (str):
        body (PutJobRegistryCredentialRequest): Job private-registry credential payload. Password
            is sealed at rest and never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobRegistryCredentialResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutJobRegistryCredentialRequest,
) -> JobRegistryCredentialResponse | Problem | None:
    """Set or replace a sealed private-registry credential for a job.

     Seals the plaintext password under namespace `registry_creds` and
    stores it against the job and registry host. Replacements do not
    consume another per-job quota slot.

    Args:
        name (str):
        body (PutJobRegistryCredentialRequest): Job private-registry credential payload. Password
            is sealed at rest and never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobRegistryCredentialResponse | Problem
    """

    return sync_detailed(
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutJobRegistryCredentialRequest,
) -> Response[JobRegistryCredentialResponse | Problem]:
    """Set or replace a sealed private-registry credential for a job.

     Seals the plaintext password under namespace `registry_creds` and
    stores it against the job and registry host. Replacements do not
    consume another per-job quota slot.

    Args:
        name (str):
        body (PutJobRegistryCredentialRequest): Job private-registry credential payload. Password
            is sealed at rest and never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[JobRegistryCredentialResponse | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PutJobRegistryCredentialRequest,
) -> JobRegistryCredentialResponse | Problem | None:
    """Set or replace a sealed private-registry credential for a job.

     Seals the plaintext password under namespace `registry_creds` and
    stores it against the job and registry host. Replacements do not
    consume another per-job quota slot.

    Args:
        name (str):
        body (PutJobRegistryCredentialRequest): Job private-registry credential payload. Password
            is sealed at rest and never returned.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        JobRegistryCredentialResponse | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            client=client,
            body=body,
        )
    ).parsed
