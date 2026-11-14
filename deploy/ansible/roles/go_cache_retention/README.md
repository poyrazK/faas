# Go cache retention

Installs `faas-go-cache-trim` and a 10-minute timer on designated native
acceptance hosts (`native-acceptance-host.yml`). The native runners
(`scripts/ci/run-native-*.sh`) keep Go build caches under
`/var/cache/faas-*/go-build`, and operators building by hand keep them under
`/root/.cache` and `/home/*/.cache`. Go itself trims an entry only after five
unused days, so on a busy host the caches outgrow the root disk and every metal
run then fails at its first write.

The helper does nothing while the root filesystem is below the high-water mark
(85%). Above it, it takes `/var/lock/faas-builder-acceptance.lock` without
waiting; when a run holds the lock it skips and the next tick retries. Holding
the lock, it removes cache entries unused for 120 h, then 48 h, 24 h, 6 h and
1 h, stopping as soon as usage is below the low-water mark (70%). Go refreshes
an entry's mtime when it uses one more than an hour old, so mtime is last use
to within the hour. A removed entry is rebuilt by the next build that needs it.

It touches only directories whose `README` is Go's own cache README, only on
the filesystem it relieves, never through a symlink, and only files named like
Go cache entries (`<2-hex>/<64-hex>-a|d`). Module caches, fixtures, workspaces
and everything else are left alone; `canary_artifact_retention` owns
`/var/tmp` workspaces.

```sh
sudo faas-go-cache-trim status   # caches found, usage, marks
sudo faas-go-cache-trim sweep    # what the timer runs
```

Each sweep writes `faas_go_cache_trim_*` gauges to the node-exporter textfile
directory: last run time, bytes and entries removed, usage after the sweep, and
`faas_go_cache_trim_outcome{outcome=...}` (`below_high_water`,
`skipped_locked`, `trimmed`, `above_low_water`). `above_low_water` means the
caches were not the problem: look at `du -xh --max-depth=2 /` next.
