from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_operation_preview import AppOperationPreview
from ...models.get_app_operation_preview_operation import (
    GetAppOperationPreviewOperation,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    operation: GetAppOperationPreviewOperation,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_operation: str = operation
    params["operation"] = json_operation

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operation-preview".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppOperationPreview | Problem | None:
    if response.status_code == 200:
        response_200 = AppOperationPreview.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppOperationPreview | Problem]:
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
    operation: GetAppOperationPreviewOperation,
    environment: str | Unset = UNSET,
) -> Response[AppOperationPreview | Problem]:
    """Preview authorization for an application operation without writing intent.

     Requires application read or deployment scope and current application access.
    Evaluates the target operation's native scope, organization role and release
    policy. Authorization is a snapshot; admission remains unknown when exact
    request validation, quotas, approval or runtime capacity need apply-time checks.
    This endpoint creates no plan, approval, reservation or customer intent.

    Args:
        slug (str):
        operation (GetAppOperationPreviewOperation):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppOperationPreview | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        operation=operation,
        environment=environment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    operation: GetAppOperationPreviewOperation,
    environment: str | Unset = UNSET,
) -> AppOperationPreview | Problem | None:
    """Preview authorization for an application operation without writing intent.

     Requires application read or deployment scope and current application access.
    Evaluates the target operation's native scope, organization role and release
    policy. Authorization is a snapshot; admission remains unknown when exact
    request validation, quotas, approval or runtime capacity need apply-time checks.
    This endpoint creates no plan, approval, reservation or customer intent.

    Args:
        slug (str):
        operation (GetAppOperationPreviewOperation):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppOperationPreview | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        operation=operation,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    operation: GetAppOperationPreviewOperation,
    environment: str | Unset = UNSET,
) -> Response[AppOperationPreview | Problem]:
    """Preview authorization for an application operation without writing intent.

     Requires application read or deployment scope and current application access.
    Evaluates the target operation's native scope, organization role and release
    policy. Authorization is a snapshot; admission remains unknown when exact
    request validation, quotas, approval or runtime capacity need apply-time checks.
    This endpoint creates no plan, approval, reservation or customer intent.

    Args:
        slug (str):
        operation (GetAppOperationPreviewOperation):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppOperationPreview | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        operation=operation,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    operation: GetAppOperationPreviewOperation,
    environment: str | Unset = UNSET,
) -> AppOperationPreview | Problem | None:
    """Preview authorization for an application operation without writing intent.

     Requires application read or deployment scope and current application access.
    Evaluates the target operation's native scope, organization role and release
    policy. Authorization is a snapshot; admission remains unknown when exact
    request validation, quotas, approval or runtime capacity need apply-time checks.
    This endpoint creates no plan, approval, reservation or customer intent.

    Args:
        slug (str):
        operation (GetAppOperationPreviewOperation):
        environment (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppOperationPreview | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            operation=operation,
            environment=environment,
        )
    ).parsed
