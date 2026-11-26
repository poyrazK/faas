# Undo project rollbacks on sustained production 5xx breaches

`gregale project rollback <slug> --yes --verify --verify-live-traffic --undo-on-live-traffic-failure` opts into automatic undo when the live-traffic gate confirms a sustained production 5xx budget breach. The reviewed plan pins this choice and the production budgets. Sparse or unavailable traffic keeps the existing gate and deadline.

The receipt saves the triggering service, the applied route generation, and the two closed windows that showed the breach. The worker closes the cutover barrier and releases unused preparations first. Before admitting undo it checks that the same route still serves and the latest closed windows still confirm a breach of the pinned budgets. If evidence no longer confirms the breach, automatic undo is blocked for operator review.

The existing checked undo restores only services that already switched, in reverse order, and links a single undo receipt atomically. Release checks, operator pauses, and serving-period checks still apply. A database trigger protects the opt-in and saved trigger evidence from older workers during rolling upgrades.
