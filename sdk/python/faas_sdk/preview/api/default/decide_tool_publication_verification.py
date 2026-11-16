from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_publication_verification import ToolPublicationVerification
from ...models.tool_publication_verification_decision import ToolPublicationVerificationDecision
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ToolPublicationVerificationDecision,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/tool-publications/{id}/verification".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ToolPublicationVerification | None:
    if response.status_code == 200:
        response_200 = ToolPublicationVerification.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolPublicationVerification]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationVerificationDecision,
) -> Response[Any | ToolPublicationVerification]:
    """Approve or revoke private candidate test authority as the personal administrator

     Requires unscoped personal administrator authority and MFA policy. Approval selects exactly one
    existing ordinary secret:MCP_* test token or managed execution OAuth connection on the candidate
    app's default environment. The exact request digest and credential authorization binding are
    immutable. Managed connections must match the reviewed issuer and resource and cover all reviewed
    scopes. Approval performs no provider request. Candidates with readiness roles also require
    approve_readiness_roles acknowledgement bound to the reviewed policy and credential authority.
    Revocation blocks subsequent credential dispatches; an accepted HTTP request may finish. Completed
    test evidence is consumed and cannot be reauthorized or revoked. This grants no invocation access.

    Args:
        id (str):
        body (ToolPublicationVerificationDecision): Owner approval selecting one sealed test token
            or managed OAuth connection, or revoking unconsumed candidate-test authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolPublicationVerification]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationVerificationDecision,
) -> Any | ToolPublicationVerification | None:
    """Approve or revoke private candidate test authority as the personal administrator

     Requires unscoped personal administrator authority and MFA policy. Approval selects exactly one
    existing ordinary secret:MCP_* test token or managed execution OAuth connection on the candidate
    app's default environment. The exact request digest and credential authorization binding are
    immutable. Managed connections must match the reviewed issuer and resource and cover all reviewed
    scopes. Approval performs no provider request. Candidates with readiness roles also require
    approve_readiness_roles acknowledgement bound to the reviewed policy and credential authority.
    Revocation blocks subsequent credential dispatches; an accepted HTTP request may finish. Completed
    test evidence is consumed and cannot be reauthorized or revoked. This grants no invocation access.

    Args:
        id (str):
        body (ToolPublicationVerificationDecision): Owner approval selecting one sealed test token
            or managed OAuth connection, or revoking unconsumed candidate-test authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolPublicationVerification
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationVerificationDecision,
) -> Response[Any | ToolPublicationVerification]:
    """Approve or revoke private candidate test authority as the personal administrator

     Requires unscoped personal administrator authority and MFA policy. Approval selects exactly one
    existing ordinary secret:MCP_* test token or managed execution OAuth connection on the candidate
    app's default environment. The exact request digest and credential authorization binding are
    immutable. Managed connections must match the reviewed issuer and resource and cover all reviewed
    scopes. Approval performs no provider request. Candidates with readiness roles also require
    approve_readiness_roles acknowledgement bound to the reviewed policy and credential authority.
    Revocation blocks subsequent credential dispatches; an accepted HTTP request may finish. Completed
    test evidence is consumed and cannot be reauthorized or revoked. This grants no invocation access.

    Args:
        id (str):
        body (ToolPublicationVerificationDecision): Owner approval selecting one sealed test token
            or managed OAuth connection, or revoking unconsumed candidate-test authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolPublicationVerification]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ToolPublicationVerificationDecision,
) -> Any | ToolPublicationVerification | None:
    """Approve or revoke private candidate test authority as the personal administrator

     Requires unscoped personal administrator authority and MFA policy. Approval selects exactly one
    existing ordinary secret:MCP_* test token or managed execution OAuth connection on the candidate
    app's default environment. The exact request digest and credential authorization binding are
    immutable. Managed connections must match the reviewed issuer and resource and cover all reviewed
    scopes. Approval performs no provider request. Candidates with readiness roles also require
    approve_readiness_roles acknowledgement bound to the reviewed policy and credential authority.
    Revocation blocks subsequent credential dispatches; an accepted HTTP request may finish. Completed
    test evidence is consumed and cannot be reauthorized or revoked. This grants no invocation access.

    Args:
        id (str):
        body (ToolPublicationVerificationDecision): Owner approval selecting one sealed test token
            or managed OAuth connection, or revoking unconsumed candidate-test authority.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolPublicationVerification
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
