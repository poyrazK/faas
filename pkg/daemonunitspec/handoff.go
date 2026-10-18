package daemonunitspec

// handoffReload is the ExecReload of a daemon that hands off to a second
// generation on `systemctl reload` (docs/adr/20261018074719-zero-downtime-daemon-restarts.md).
const handoffReload = "/bin/kill -USR2 $MAINPID"

// HandsOffOnReload reports whether daemon's unit reloads by a two-generation
// hand-off, so a deploy can reload it instead of restarting it.
func HandsOffOnReload(daemon string) bool {
	for _, e := range append(append([]Entry(nil), Registry...), OptionalRegistry...) {
		if e.Name == daemon {
			return e.Unit().ExecReload == handoffReload
		}
	}
	return false
}
