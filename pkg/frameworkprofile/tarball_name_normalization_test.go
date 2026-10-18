// adr: 088
package frameworkprofile

import "testing"

func TestProfileArchiveRepeatedLeadingDot(t *testing.T) {
	for _, root := range []string{"", "apps/api"} {
		t.Run("source root="+root, func(t *testing.T) {
			prefix := "././repo/"
			if root != "" {
				prefix += root + "/"
			}
			archive := writeProfileArchive(t, map[string]string{
				prefix + "package.json": `{"dependencies":{"express":"^5"},"scripts":{"start":"node server.js"}}`,
				prefix + "server.js":    "app.listen(process.env.PORT);\n",
			})
			profile, err := AnalyzeTarballAtRoot(archive, root)
			if err != nil || profile.Framework != "express" || profile.StartCommand != "npm run start" {
				t.Errorf("profile = %+v, %v; want express with npm run start", profile, err)
			}
		})
	}
}
