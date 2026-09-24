from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response import DeploymentResponse
from ...models.problem import Problem
from ...models.rollback_request import RollbackRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: RollbackRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/rollback".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponse | Problem | None:
    if response.status_code == 202:
        response_202 = DeploymentResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[DeploymentResponse | Problem]:
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
    body: RollbackRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Roll back to the previous deployment, or to a specific historical deployment.

     Without a request body, rolls back to the most-recent superseded
    deployment (the pre-#976 behaviour).

    With `target_deployment_id` in the body, rolls back to the
    named deployment. The id must belong to this app and the row
    must have `status='superseded'`. Rolling back to the
    already-current live deployment is rejected (409
    `rollback_target_already_live`). A target whose snapshot has
    been garbage-collected is rejected (409
    `rollback_target_snapshot_expired`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RollbackRequest | Unset): Body for POST /v1/apps/{slug}/rollback (SAFE-RELEASES-G,
            issue #976). All fields optional. Without a body the handler falls back to rolling back to
            the most-recent superseded deployment (pre-#976 behaviour). With `target_deployment_id`
            set, the handler validates that the named deployment belongs to this app and is superseded
            or live with zero traffic.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Roll back to the previous deployment, or to a specific historical deployment.

     Without a request body, rolls back to the most-recent superseded
    deployment (the pre-#976 behaviour).

    With `target_deployment_id` in the body, rolls back to the
    named deployment. The id must belong to this app and the row
    must have `status='superseded'`. Rolling back to the
    already-current live deployment is rejected (409
    `rollback_target_already_live`). A target whose snapshot has
    been garbage-collected is rejected (409
    `rollback_target_snapshot_expired`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RollbackRequest | Unset): Body for POST /v1/apps/{slug}/rollback (SAFE-RELEASES-G,
            issue #976). All fields optional. Without a body the handler falls back to rolling back to
            the most-recent superseded deployment (pre-#976 behaviour). With `target_deployment_id`
            set, the handler validates that the named deployment belongs to this app and is superseded
            or live with zero traffic.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[DeploymentResponse | Problem]:
    """Roll back to the previous deployment, or to a specific historical deployment.

     Without a request body, rolls back to the most-recent superseded
    deployment (the pre-#976 behaviour).

    With `target_deployment_id` in the body, rolls back to the
    named deployment. The id must belong to this app and the row
    must have `status='superseded'`. Rolling back to the
    already-current live deployment is rejected (409
    `rollback_target_already_live`). A target whose snapshot has
    been garbage-collected is rejected (409
    `rollback_target_snapshot_expired`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RollbackRequest | Unset): Body for POST /v1/apps/{slug}/rollback (SAFE-RELEASES-G,
            issue #976). All fields optional. Without a body the handler falls back to rolling back to
            the most-recent superseded deployment (pre-#976 behaviour). With `target_deployment_id`
            set, the handler validates that the named deployment belongs to this app and is superseded
            or live with zero traffic.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: RollbackRequest | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> DeploymentResponse | Problem | None:
    """Roll back to the previous deployment, or to a specific historical deployment.

     Without a request body, rolls back to the most-recent superseded
    deployment (the pre-#976 behaviour).

    With `target_deployment_id` in the body, rolls back to the
    named deployment. The id must belong to this app and the row
    must have `status='superseded'`. Rolling back to the
    already-current live deployment is rejected (409
    `rollback_target_already_live`). A target whose snapshot has
    been garbage-collected is rejected (409
    `rollback_target_snapshot_expired`).

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (RollbackRequest | Unset): Body for POST /v1/apps/{slug}/rollback (SAFE-RELEASES-G,
            issue #976). All fields optional. Without a body the handler falls back to rolling back to
            the most-recent superseded deployment (pre-#976 behaviour). With `target_deployment_id`
            set, the handler validates that the named deployment belongs to this app and is superseded
            or live with zero traffic.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
