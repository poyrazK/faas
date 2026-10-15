from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_version import ApplicationStandardVersion
from ...models.create_application_standard_version_request import CreateApplicationStandardVersionRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    standard: str,
    *,
    body: CreateApplicationStandardVersionRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/application-standards/{standard}/versions".format(
            slug=quote(str(slug), safe=""),
            standard=quote(str(standard), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardVersion | Problem | None:
    if response.status_code == 201:
        response_201 = ApplicationStandardVersion.from_dict(response.json())

        return response_201

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
) -> Response[ApplicationStandardVersion | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardVersionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardVersion | Problem]:
    """Publish a candidate application standard version.

     Requires org.manage_application_standards (owner or admin), MFA and deploy-write scope.
    expected_version is 0 for a new standard and the latest version for an update.
    Publication creates immutable history and does not change an assignment or application.
    A concurrent publication returns application_standard_version_stale without writes.

    Args:
        slug (str):
        standard (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardVersionRequest): A strictly decoded candidate publication
            with an explicit concurrency version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        standard=standard,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardVersionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardVersion | Problem | None:
    """Publish a candidate application standard version.

     Requires org.manage_application_standards (owner or admin), MFA and deploy-write scope.
    expected_version is 0 for a new standard and the latest version for an update.
    Publication creates immutable history and does not change an assignment or application.
    A concurrent publication returns application_standard_version_stale without writes.

    Args:
        slug (str):
        standard (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardVersionRequest): A strictly decoded candidate publication
            with an explicit concurrency version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardVersion | Problem
    """

    return sync_detailed(
        slug=slug,
        standard=standard,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardVersionRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplicationStandardVersion | Problem]:
    """Publish a candidate application standard version.

     Requires org.manage_application_standards (owner or admin), MFA and deploy-write scope.
    expected_version is 0 for a new standard and the latest version for an update.
    Publication creates immutable history and does not change an assignment or application.
    A concurrent publication returns application_standard_version_stale without writes.

    Args:
        slug (str):
        standard (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardVersionRequest): A strictly decoded candidate publication
            with an explicit concurrency version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        standard=standard,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateApplicationStandardVersionRequest,
    idempotency_key: str | Unset = UNSET,
) -> ApplicationStandardVersion | Problem | None:
    """Publish a candidate application standard version.

     Requires org.manage_application_standards (owner or admin), MFA and deploy-write scope.
    expected_version is 0 for a new standard and the latest version for an update.
    Publication creates immutable history and does not change an assignment or application.
    A concurrent publication returns application_standard_version_stale without writes.

    Args:
        slug (str):
        standard (str):
        idempotency_key (str | Unset):
        body (CreateApplicationStandardVersionRequest): A strictly decoded candidate publication
            with an explicit concurrency version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardVersion | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            standard=standard,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
