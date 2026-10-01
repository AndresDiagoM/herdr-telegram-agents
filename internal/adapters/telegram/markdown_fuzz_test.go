package telegram

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// fuzzTag matches one tag in the rendered output.
var fuzzTag = regexp.MustCompile(`<(/?)([a-z]+)([^<>]*)>`)

// checkTelegramHTML reports why s is not well-formed in Telegram's HTML
// subset: unknown tags, unbalanced nesting, a stray '<' or '>', or a link
// that is not http(s).
func checkTelegramHTML(s string) string {
	allowed := map[string]bool{"b": true, "i": true, "s": true, "a": true, "code": true, "pre": true}
	var stack []string
	last := 0
	for _, m := range fuzzTag.FindAllStringSubmatchIndex(s, -1) {
		if text := s[last:m[0]]; strings.ContainsAny(text, "<>") {
			return "stray angle bracket in " + text
		}
		last = m[1]
		closing, name, attrs := s[m[2]:m[3]] == "/", s[m[4]:m[5]], s[m[6]:m[7]]
		if !allowed[name] {
			return "tag " + name
		}
		if closing {
			if len(stack) == 0 || stack[len(stack)-1] != name {
				return "unbalanced </" + name + ">"
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if name == "a" && !strings.HasPrefix(attrs, ` href="https://`) && !strings.HasPrefix(attrs, ` href="http://`) {
			return "link " + attrs
		}
		stack = append(stack, name)
	}
	if strings.ContainsAny(s[last:], "<>") {
		return "stray angle bracket at the end"
	}
	if len(stack) != 0 {
		return "unclosed " + strings.Join(stack, ",")
	}
	return ""
}

// FuzzRenderMarkdown: whatever an agent writes, every rendered part is
// well-formed Telegram HTML with http(s) links only.
func FuzzRenderMarkdown(f *testing.F) {
	for _, seed := range []string{stressSample, "[x](tg://a)", "**a *b** c*", "```go\nx\n```", "| a | b |\n|---|---|\n| 1 | 2 |", "> q\n- a\n  - b", "[https://a.b](https://c.d/x)", "`a<b>`", "~~x~~ __y__"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if !utf8.ValidString(in) {
			t.Skip()
		}
		for _, part := range splitMarkdown(in, 4096) {
			out := renderMarkdown(part)
			if why := checkTelegramHTML(out); why != "" {
				t.Fatalf("%s\n in  %q\n out %q", why, part, out)
			}
		}
	})
}

func TestCheckTelegramHTMLRejects(t *testing.T) {
	for _, bad := range []string{`<a href="tg://x">x</a>`, `<b>x`, `<b><i>x</b></i>`, `<script>x</script>`, `a < b`} {
		if checkTelegramHTML(bad) == "" {
			t.Errorf("accepted %q", bad)
		}
	}
	if why := checkTelegramHTML(`<b>a</b> <a href="https://x.y">x</a> (x.y) <pre><code class="language-go">a &lt; b</code></pre>`); why != "" {
		t.Errorf("rejected valid HTML: %s", why)
	}
}
