from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cancel_deployment_request import CancelDeploymentRequest
from ...models.cancel_deployment_response_200 import CancelDeploymentResponse200
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: CancelDeploymentRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/{id}/cancel".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | CancelDeploymentResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = CancelDeploymentResponse200.from_dict(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | CancelDeploymentResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelDeploymentRequest | Unset = UNSET,
) -> Response[Any | CancelDeploymentResponse200 | Problem]:
    r"""Cancel a deployment.

     ADR-124 deployment queue controls — flip a deployment in
    {pending, building, imaging, snapshotting} to \"cancelled\"
    and cascade-cancel its in-flight builds. Live deployments
    return 409 `deployment_cancel_live_forbidden` with the
    rollback hint. Optional reason: user | auto_quota |
    auto_health | system.

    Args:
        slug (str):
        id (str):
        body (CancelDeploymentRequest | Unset): Body for POST
            /v1/apps/{slug}/deployments/{id}/cancel (ADR-124). Optional — empty body defaults to
            reason='user' server-side. Reason is the closed set user | auto_quota | auto_health |
            system.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | CancelDeploymentResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelDeploymentRequest | Unset = UNSET,
) -> Any | CancelDeploymentResponse200 | Problem | None:
    r"""Cancel a deployment.

     ADR-124 deployment queue controls — flip a deployment in
    {pending, building, imaging, snapshotting} to \"cancelled\"
    and cascade-cancel its in-flight builds. Live deployments
    return 409 `deployment_cancel_live_forbidden` with the
    rollback hint. Optional reason: user | auto_quota |
    auto_health | system.

    Args:
        slug (str):
        id (str):
        body (CancelDeploymentRequest | Unset): Body for POST
            /v1/apps/{slug}/deployments/{id}/cancel (ADR-124). Optional — empty body defaults to
            reason='user' server-side. Reason is the closed set user | auto_quota | auto_health |
            system.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | CancelDeploymentResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelDeploymentRequest | Unset = UNSET,
) -> Response[Any | CancelDeploymentResponse200 | Problem]:
    r"""Cancel a deployment.

     ADR-124 deployment queue controls — flip a deployment in
    {pending, building, imaging, snapshotting} to \"cancelled\"
    and cascade-cancel its in-flight builds. Live deployments
    return 409 `deployment_cancel_live_forbidden` with the
    rollback hint. Optional reason: user | auto_quota |
    auto_health | system.

    Args:
        slug (str):
        id (str):
        body (CancelDeploymentRequest | Unset): Body for POST
            /v1/apps/{slug}/deployments/{id}/cancel (ADR-124). Optional — empty body defaults to
            reason='user' server-side. Reason is the closed set user | auto_quota | auto_health |
            system.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | CancelDeploymentResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: CancelDeploymentRequest | Unset = UNSET,
) -> Any | CancelDeploymentResponse200 | Problem | None:
    r"""Cancel a deployment.

     ADR-124 deployment queue controls — flip a deployment in
    {pending, building, imaging, snapshotting} to \"cancelled\"
    and cascade-cancel its in-flight builds. Live deployments
    return 409 `deployment_cancel_live_forbidden` with the
    rollback hint. Optional reason: user | auto_quota |
    auto_health | system.

    Args:
        slug (str):
        id (str):
        body (CancelDeploymentRequest | Unset): Body for POST
            /v1/apps/{slug}/deployments/{id}/cancel (ADR-124). Optional — empty body defaults to
            reason='user' server-side. Reason is the closed set user | auto_quota | auto_health |
            system.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | CancelDeploymentResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
