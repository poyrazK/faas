// adr: 20261123180000

package main

// GuideSearchCommands returns the command lines `gregale guide "<query>"`
// lists, best first, without reading the network.
func GuideSearchCommands(query string) []string {
	var out []string
	for _, hit := range searchGuideCommands(query) {
		out = append(out, hit.Command)
	}
	return out
}
