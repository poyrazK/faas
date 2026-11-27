# Functions, queues and crons

A function is a handler(event, ctx) in functions/<name>/; its config in code names the queue or cron that calls it.

- Write a queue-triggered function with a test:
    gregale add function <name> --trigger queue
- Write a cron-triggered function:
    gregale add function <name> --trigger cron --schedule "0 8 * * *"
- Deploy every function in the repo:
    gregale deploy
- Send a message:
    gregale queue send <app> --queue-name <queue> --payload '<json>'
- See queue depth, bindings and consumer liveness:
    gregale queue status <app>
- List and replay dead letters:
    gregale dlq list <app>
- List an app's crons:
    gregale cron list <app>
- Show one cron's runs (its ID is in the list):
    gregale cron runs <cron-id>

Notes:
- A handler that answers 4xx (other than 408 or 429) dead-letters the message at once; 5xx, 408 and 429 are retried up to max_attempts.
- An HTTP app cannot consume a queue: use a function, or a worker app with a pull binding.
- Every path of a function is its handler, so canary probes skip a function unless it sets release_probe_paths.
