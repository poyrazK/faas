# Automatically undo a project rollback after confirmed health failure

Status: Accepted

`gregale project rollback <slug> --yes --verify --undo-on-health-failure` opts into automatic undo after an attributed probe returns 5xx from a still-current routing generation. The preview binds this option into its plan hash. Timeouts, other probe failures, and uncertain or changed routing evidence leave the rollback paused.

After saving the failure, the worker closes the cutover gate and releases unused preparations. It checks the failed serving generation and the targeted serving revisions again. If the inverse passes the existing undo preflight, the store atomically retires the original running operation and links one inverse. The inverse restores the exact pre-rollback revisions in reverse cutover order.

An operator pause can fence the automatic undo before admission. Release commands that need acknowledgement leave the undo paused for explicit review. The original receipt records the probe failure, the automatic undo status, and any linked operation ID. Follow that ID with `--status`.
