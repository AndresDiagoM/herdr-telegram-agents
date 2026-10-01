package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// fuzzSecrets are values that must never survive redaction when they stand
// as a separate word, with the part that must disappear; built from parts
// so secret scanners ignore them.
var fuzzSecrets = []struct{ text, sensitive string }{
	{"sk-" + "proj-abcdefghijklmnopqrstuvwxyz0123", "mnopqrstuvwxyz"},
	{"ghp_" + "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", "QRSTUVWXYZabcd"},
	{"AKIA" + "IOSFODNN7EXAMPLE", "IOSFODNN7EX"},
	{"xoxb-" + "123456789012-abcdefghijklmnop", "123456789012-abc"},
	{"glpat-" + "abcdefghijklmnopqrstuv", "abcdefghijklmnop"},
	{"sk_live_" + "abcdefghijklmnopqrstuvwx", "abcdefghijklmnop"},
	{"DB_PASSWORD=" + "supersecret1", "supersecret1"},
	{"postgres://app:" + "pa55word@db.local/app", "pa55word"},
	{botToken, botToken[11:]},
}

// FuzzRedact: whatever surrounds them, separated secrets vanish, the output
// stays valid UTF-8, and a second pass changes nothing.
func FuzzRedact(f *testing.F) {
	for _, seed := range []string{"", "token: none", "<pre>x</pre>", "password=", "a\nb", "é ü 日本", "https://x.y/a?b=1&c=2"} {
		f.Add(seed, seed, uint8(0))
	}
	r := domain.NewRedactor(botToken)
	f.Fuzz(func(t *testing.T, before, after string, pick uint8) {
		if !utf8.ValidString(before) || !utf8.ValidString(after) {
			t.Skip()
		}
		secret := fuzzSecrets[int(pick)%len(fuzzSecrets)]
		in := before + " " + secret.text + " " + after
		out, _ := r.Redact(in)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 from %q", in)
		}
		core := secret.sensitive
		if strings.Contains(out, core) && !strings.Contains(before+after, core) {
			t.Fatalf("secret survived:\n in  %q\n out %q", in, out)
		}
		again, stats := r.Redact(out)
		if again != out || stats.Total() != 0 {
			t.Fatalf("not idempotent:\n once  %q\n twice %q (%v)", out, again, stats)
		}
	})
}
