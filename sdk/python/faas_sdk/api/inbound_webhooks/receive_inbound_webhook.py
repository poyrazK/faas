from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.inbound_webhook_receipt_response import InboundWebhookReceiptResponse
from ...models.problem import Problem
from ...models.receive_inbound_webhook_body import ReceiveInboundWebhookBody
from ...models.workflow_callback_webhook_receipt_response import WorkflowCallbackWebhookReceiptResponse
from ...types import Response


def _get_kwargs(
    token: str,
    *,
    body: ReceiveInboundWebhookBody,
    stripe_signature: str,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Stripe-Signature"] = stripe_signature

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/hooks/{token}".format(
            token=quote(str(token), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem | None:
    if response.status_code == 202:

        def _parse_response_202(data: object) -> InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                response_202_type_0 = InboundWebhookReceiptResponse.from_dict(data)

                return response_202_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            response_202_type_1 = WorkflowCallbackWebhookReceiptResponse.from_dict(data)

            return response_202_type_1

        response_202 = _parse_response_202(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReceiveInboundWebhookBody,
    stripe_signature: str,
) -> Response[InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem]:
    """Verify and durably accept a provider webhook.

     This route does not use a Gregale bearer key. The opaque URL and the
    provider signature are the trust boundary. For Stripe, the exact raw
    body is verified against Stripe-Signature. An exact workflow callback
    binding completes its callback durably instead of enqueuing an app
    invocation. Unmatched events keep the ordinary invocation path.
    Terminal callbacks are acknowledged as ignored after verification.

    Args:
        token (str):
        stripe_signature (str):
        body (ReceiveInboundWebhookBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        token=token,
        body=body,
        stripe_signature=stripe_signature,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReceiveInboundWebhookBody,
    stripe_signature: str,
) -> InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem | None:
    """Verify and durably accept a provider webhook.

     This route does not use a Gregale bearer key. The opaque URL and the
    provider signature are the trust boundary. For Stripe, the exact raw
    body is verified against Stripe-Signature. An exact workflow callback
    binding completes its callback durably instead of enqueuing an app
    invocation. Unmatched events keep the ordinary invocation path.
    Terminal callbacks are acknowledged as ignored after verification.

    Args:
        token (str):
        stripe_signature (str):
        body (ReceiveInboundWebhookBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem
    """

    return sync_detailed(
        token=token,
        client=client,
        body=body,
        stripe_signature=stripe_signature,
    ).parsed


async def asyncio_detailed(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReceiveInboundWebhookBody,
    stripe_signature: str,
) -> Response[InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem]:
    """Verify and durably accept a provider webhook.

     This route does not use a Gregale bearer key. The opaque URL and the
    provider signature are the trust boundary. For Stripe, the exact raw
    body is verified against Stripe-Signature. An exact workflow callback
    binding completes its callback durably instead of enqueuing an app
    invocation. Unmatched events keep the ordinary invocation path.
    Terminal callbacks are acknowledged as ignored after verification.

    Args:
        token (str):
        stripe_signature (str):
        body (ReceiveInboundWebhookBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem]
    """

    kwargs = _get_kwargs(
        token=token,
        body=body,
        stripe_signature=stripe_signature,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    token: str,
    *,
    client: AuthenticatedClient | Client,
    body: ReceiveInboundWebhookBody,
    stripe_signature: str,
) -> InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem | None:
    """Verify and durably accept a provider webhook.

     This route does not use a Gregale bearer key. The opaque URL and the
    provider signature are the trust boundary. For Stripe, the exact raw
    body is verified against Stripe-Signature. An exact workflow callback
    binding completes its callback durably instead of enqueuing an app
    invocation. Unmatched events keep the ordinary invocation path.
    Terminal callbacks are acknowledged as ignored after verification.

    Args:
        token (str):
        stripe_signature (str):
        body (ReceiveInboundWebhookBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InboundWebhookReceiptResponse | WorkflowCallbackWebhookReceiptResponse | Problem
    """

    return (
        await asyncio_detailed(
            token=token,
            client=client,
            body=body,
            stripe_signature=stripe_signature,
        )
    ).parsed
