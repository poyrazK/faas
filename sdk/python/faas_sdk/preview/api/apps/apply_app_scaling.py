from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_scaling_receipt import AppScalingReceipt
from ...models.apply_app_scaling_request import ApplyAppScalingRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ApplyAppScalingRequest,
    environment: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["environment"] = environment

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/scaling".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppScalingReceipt | Problem | None:
    if response.status_code == 200:
        response_200 = AppScalingReceipt.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppScalingReceipt | Problem]:
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
    body: ApplyAppScalingRequest,
    environment: str | Unset = UNSET,
) -> Response[AppScalingReceipt | Problem]:
    """Commit reviewed scaling settings with a recoverable request ID.

     Requires deploy:write and the same MFA, plan and capacity admission as app settings updates.
    The expected app UUID and revision are checked under the write lock. Default revisions advance for
    every accepted request and for legacy changes to RAM, CPU, concurrency or min instances.
    Named environments use their workload revision, including no-op revision semantics.
    Settings and the receipt commit atomically. Reuse request_id with the exact original
    body after an interruption to recover the original receipt even after later writes.
    Changing the body under an accepted ID returns scaling_request_conflict; a stale
    revision returns scaling_revision_conflict. Receipts survive until the app is purged.
    Named production and protected environments require their existing approved workflow.
    A receipt confirms desired configuration only; named settings require a subsequent
    deployment, and default settings do not prove guest readiness or replica convergence.

    Args:
        slug (str):
        environment (str | Unset):
        body (ApplyAppScalingRequest): A reviewed partial scaling change with a durable request
            identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppScalingReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
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
    body: ApplyAppScalingRequest,
    environment: str | Unset = UNSET,
) -> AppScalingReceipt | Problem | None:
    """Commit reviewed scaling settings with a recoverable request ID.

     Requires deploy:write and the same MFA, plan and capacity admission as app settings updates.
    The expected app UUID and revision are checked under the write lock. Default revisions advance for
    every accepted request and for legacy changes to RAM, CPU, concurrency or min instances.
    Named environments use their workload revision, including no-op revision semantics.
    Settings and the receipt commit atomically. Reuse request_id with the exact original
    body after an interruption to recover the original receipt even after later writes.
    Changing the body under an accepted ID returns scaling_request_conflict; a stale
    revision returns scaling_revision_conflict. Receipts survive until the app is purged.
    Named production and protected environments require their existing approved workflow.
    A receipt confirms desired configuration only; named settings require a subsequent
    deployment, and default settings do not prove guest readiness or replica convergence.

    Args:
        slug (str):
        environment (str | Unset):
        body (ApplyAppScalingRequest): A reviewed partial scaling change with a durable request
            identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppScalingReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        environment=environment,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyAppScalingRequest,
    environment: str | Unset = UNSET,
) -> Response[AppScalingReceipt | Problem]:
    """Commit reviewed scaling settings with a recoverable request ID.

     Requires deploy:write and the same MFA, plan and capacity admission as app settings updates.
    The expected app UUID and revision are checked under the write lock. Default revisions advance for
    every accepted request and for legacy changes to RAM, CPU, concurrency or min instances.
    Named environments use their workload revision, including no-op revision semantics.
    Settings and the receipt commit atomically. Reuse request_id with the exact original
    body after an interruption to recover the original receipt even after later writes.
    Changing the body under an accepted ID returns scaling_request_conflict; a stale
    revision returns scaling_revision_conflict. Receipts survive until the app is purged.
    Named production and protected environments require their existing approved workflow.
    A receipt confirms desired configuration only; named settings require a subsequent
    deployment, and default settings do not prove guest readiness or replica convergence.

    Args:
        slug (str):
        environment (str | Unset):
        body (ApplyAppScalingRequest): A reviewed partial scaling change with a durable request
            identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppScalingReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        environment=environment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApplyAppScalingRequest,
    environment: str | Unset = UNSET,
) -> AppScalingReceipt | Problem | None:
    """Commit reviewed scaling settings with a recoverable request ID.

     Requires deploy:write and the same MFA, plan and capacity admission as app settings updates.
    The expected app UUID and revision are checked under the write lock. Default revisions advance for
    every accepted request and for legacy changes to RAM, CPU, concurrency or min instances.
    Named environments use their workload revision, including no-op revision semantics.
    Settings and the receipt commit atomically. Reuse request_id with the exact original
    body after an interruption to recover the original receipt even after later writes.
    Changing the body under an accepted ID returns scaling_request_conflict; a stale
    revision returns scaling_revision_conflict. Receipts survive until the app is purged.
    Named production and protected environments require their existing approved workflow.
    A receipt confirms desired configuration only; named settings require a subsequent
    deployment, and default settings do not prove guest readiness or replica convergence.

    Args:
        slug (str):
        environment (str | Unset):
        body (ApplyAppScalingRequest): A reviewed partial scaling change with a durable request
            identity.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppScalingReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            environment=environment,
        )
    ).parsed
