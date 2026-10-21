package gateway

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// htmlTemplatePage is the page as html/template rendered it before
// renderChallengePage; tests may execute templates, daemons may not.
var htmlTemplatePage = template.Must(template.New("challenge").Parse(challengePageParts[0] + "{{.Nonce}}" +
	challengePageParts[1] + "{{.Action}}" + challengePageParts[2] + "{{.Token}}" + challengePageParts[3] +
	"{{.Difficulty}}" + challengePageParts[4] + "{{.Token}}" + challengePageParts[5] + "{{.Return}}" +
	challengePageParts[6] + "{{.Nonce}}" + challengePageParts[7]))

// tokens is the page as a browser's tokenizer sees it: tags with their
// decoded attributes, and text.
func tokens(t *testing.T, page []byte) []string {
	t.Helper()
	var out []string
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.TextToken:
			out = append(out, "text:"+string(z.Text()))
		default:
			tok := z.Token()
			parts := []string{tok.Type.String(), tok.Data}
			for _, a := range tok.Attr {
				parts = append(parts, a.Key+"="+a.Val)
			}
			out = append(out, strings.Join(parts, " "))
		}
	}
}

// TestChallengePageMatchesHTMLTemplate: without html/template the page
// still decodes, in a browser, to what html/template produced, for hostile
// values too, and a value never ends its attribute.
func TestChallengePageMatchesHTMLTemplate(t *testing.T) {
	for _, d := range []challengePageData{
		{Nonce: "q+Lk9/zZ0a1b2c3d4e5f6A", Action: ChallengeSolvePath, Token: "c1.abc-def_ghi", Difficulty: 18, Return: "/account?tab=orders&x=1"},
		{Nonce: "n", Action: ChallengeSolvePath, Token: `c1."><script>alert(1)</script>`, Difficulty: 0,
			Return: `/"><img src=x onerror=alert(1)>&amp;'`},
		{Nonce: `"'<>&`, Action: ChallengeSolvePath, Token: "", Difficulty: 31, Return: "/ünïcode/路径?q=a+b"},
	} {
		t.Run(fmt.Sprintf("%.20s", d.Return), func(t *testing.T) {
			var got, want bytes.Buffer
			if err := renderChallengePage(&got, d); err != nil {
				t.Fatal(err)
			}
			if err := htmlTemplatePage.Execute(&want, d); err != nil {
				t.Fatal(err)
			}
			g, w := tokens(t, got.Bytes()), tokens(t, want.Bytes())
			if strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Fatalf("page tokens differ from html/template's:\ngot  %q\nwant %q", g, w)
			}
			for _, tok := range g {
				if strings.HasPrefix(tok, "start img") || strings.Contains(tok, "onerror") {
					t.Fatalf("a value broke out of its attribute: %s", tok)
				}
			}
		})
	}
}
