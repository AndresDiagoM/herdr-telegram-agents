package cli

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCRLFWriter(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{"bare newlines", []string{"a\nb\n"}, "a\r\nb\r\n"},
		{"already crlf", []string{"a\r\n"}, "a\r\n"},
		{"cr and lf split across writes", []string{"a\r", "\nb"}, "a\r\nb"},
		{"newline first", []string{"\n\n"}, "\r\n\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := &crlfWriter{w: &buf}
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if err != nil || n != len(s) {
					t.Fatalf("Write(%q) = %d, %v", s, n, err)
				}
			}
			if buf.String() != tt.want {
				t.Fatalf("got %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

// TestRawPaneOutsideTerminal: tests, pipes and files keep the pane as is.
func TestRawPaneOutsideTerminal(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, in := range []io.Reader{strings.NewReader("q"), f} {
		rc := &runContext{stdin: in, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, log: slog.New(slog.DiscardHandler)}
		pane, restore := rawPane(rc)
		if pane != rc {
			t.Fatalf("%T: pane context replaced outside a terminal", in)
		}
		restore()
		restore()
	}
}
