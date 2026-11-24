from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.object_workload_credential_request import ObjectWorkloadCredentialRequest
from ...models.object_workload_credential_response import ObjectWorkloadCredentialResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: ObjectWorkloadCredentialRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/workload/object-storage/credentials",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectWorkloadCredentialResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ObjectWorkloadCredentialResponse.from_dict(response.json())

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
) -> Response[ObjectWorkloadCredentialResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: ObjectWorkloadCredentialRequest,
) -> Response[ObjectWorkloadCredentialResponse | Problem]:
    """Trade a binding token for a one-hour S3 credential on a granted bucket.

     Called by guest-init's loopback credential endpoint, not by customer
    code. A live production instance presents its gregale:bindings
    workload token and names a bucket its app's gregale.yaml `uses:`
    grants (or omits the bucket when it holds exactly one bucket grant).
    The answer is the AWS container credential shape, limited to the
    grant's access and key prefix, and expires within the hour. Each
    instance holds at most 8 live credentials per bucket. Gated by the
    cross_app_bindings platform flag (off: 404).

    Args:
        body (ObjectWorkloadCredentialRequest): Body for POST /v1/workload/object-
            storage/credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectWorkloadCredentialResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: ObjectWorkloadCredentialRequest,
) -> ObjectWorkloadCredentialResponse | Problem | None:
    """Trade a binding token for a one-hour S3 credential on a granted bucket.

     Called by guest-init's loopback credential endpoint, not by customer
    code. A live production instance presents its gregale:bindings
    workload token and names a bucket its app's gregale.yaml `uses:`
    grants (or omits the bucket when it holds exactly one bucket grant).
    The answer is the AWS container credential shape, limited to the
    grant's access and key prefix, and expires within the hour. Each
    instance holds at most 8 live credentials per bucket. Gated by the
    cross_app_bindings platform flag (off: 404).

    Args:
        body (ObjectWorkloadCredentialRequest): Body for POST /v1/workload/object-
            storage/credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectWorkloadCredentialResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: ObjectWorkloadCredentialRequest,
) -> Response[ObjectWorkloadCredentialResponse | Problem]:
    """Trade a binding token for a one-hour S3 credential on a granted bucket.

     Called by guest-init's loopback credential endpoint, not by customer
    code. A live production instance presents its gregale:bindings
    workload token and names a bucket its app's gregale.yaml `uses:`
    grants (or omits the bucket when it holds exactly one bucket grant).
    The answer is the AWS container credential shape, limited to the
    grant's access and key prefix, and expires within the hour. Each
    instance holds at most 8 live credentials per bucket. Gated by the
    cross_app_bindings platform flag (off: 404).

    Args:
        body (ObjectWorkloadCredentialRequest): Body for POST /v1/workload/object-
            storage/credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectWorkloadCredentialResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: ObjectWorkloadCredentialRequest,
) -> ObjectWorkloadCredentialResponse | Problem | None:
    """Trade a binding token for a one-hour S3 credential on a granted bucket.

     Called by guest-init's loopback credential endpoint, not by customer
    code. A live production instance presents its gregale:bindings
    workload token and names a bucket its app's gregale.yaml `uses:`
    grants (or omits the bucket when it holds exactly one bucket grant).
    The answer is the AWS container credential shape, limited to the
    grant's access and key prefix, and expires within the hour. Each
    instance holds at most 8 live credentials per bucket. Gated by the
    cross_app_bindings platform flag (off: 404).

    Args:
        body (ObjectWorkloadCredentialRequest): Body for POST /v1/workload/object-
            storage/credentials.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectWorkloadCredentialResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
