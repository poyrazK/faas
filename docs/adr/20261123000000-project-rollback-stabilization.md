# Require a post-cutover health stabilization window

`gregale project rollback <slug> --yes --verify --stabilize-for 30s` keeps each subsequent cutover on hold while the restored service passes repeated configured HTTP probes. The last verified service must also finish its window before the rollback completes. The option accepts whole seconds from 1s to 60s and is included in the reviewed plan.

The worker waits for gateway acknowledgements, probes the exact restored revision and checks the saved route generation after each success. The receipt records the start of the healthy window and the last probe time. Rounds are spaced at least five seconds apart. An unattributed result or a gap longer than 15 seconds restarts the window. The project deadline includes the health verification budget and requested stabilization duration for each pending health gate.

An attributed non-2xx response blocks further cutovers. A confirmed 5xx can still trigger automatic undo if enabled. The cutover transaction checks that saved probe results span the requested window and a database trigger protects the policy during rolling upgrades.
