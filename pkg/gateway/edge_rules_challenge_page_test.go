package gateway

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/onebox-faas/faas/pkg/api"
)

// htmlTemplatePage is the page as html/template rendered it before
// renderChallengePage; tests may execute templates, daemons may not.
var htmlTemplatePage = template.Must(template.New("challenge").Parse(challengePageParts[0] + "{{.Nonce}}" +
	challengePageParts[1] + "{{.Reference}}" + challengePageParts[2] + "{{.Action}}" + challengePageParts[3] +
	"{{.Token}}" + challengePageParts[4] + "{{.Difficulty}}" + challengePageParts[5] + "{{.Token}}" +
	challengePageParts[6] + "{{.Return}}" + challengePageParts[7] + "{{.Nonce}}" + challengePageParts[8]))

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
		{Nonce: "n", Action: ChallengeSolvePath, Token: "c1.t", Difficulty: 16, Return: "/ref",
			Reference: ` If you can't, give the site's owner this reference: </noscript><script>alert(1)</script>&.`},
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
			// A value that broke out of its attribute would add a tag;
			// inside one, the tokenizer hands it back as the decoded value.
			for _, tok := range g {
				if strings.HasPrefix(tok, "StartTag img") || strings.HasPrefix(tok, "StartTag script") && !strings.Contains(tok, "nonce=") {
					t.Fatalf("a value broke out of its attribute: %s", tok)
				}
			}
			if !strings.Contains(strings.Join(g, "\n"), "name=return value="+d.Return) {
				t.Errorf("the return path does not decode back to %q", d.Return)
			}
		})
	}
}

// TestChallengePageReference: a visitor without JavaScript is shown the
// request ID, which the owner finds on the rule's challenged event (ADR-831
// amendment 8); without one the sentence is left out.
func TestChallengePageReference(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"req-7f3a", "Turn on JavaScript to continue to this site. If you can't, give the site's owner this reference: req-7f3a."},
		{"", "Turn on JavaScript to continue to this site."},
	} {
		r := browserRequest("GET", "/shop")
		if tc.id != "" {
			r.Header.Set(api.RequestIDHeader, tc.id)
		}
		w := httptest.NewRecorder()
		writeChallengePage(w, r, "c1.t", 16)
		if !strings.Contains(html.UnescapeString(w.Body.String()), tc.want+"</p></noscript>") {
			t.Errorf("id %q: page lacks %q:\n%s", tc.id, tc.want, w.Body.String())
		}
	}
}
