package engine

import (
	"strings"
	"testing"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
)

func TestDataTableHasNoColumnPadding(t *testing.T) {
	input := `<table>
		<tr><th>Key</th><th>Description</th></tr>
		<tr><td>a</td><td>A very long description that would otherwise pad every other cell with spaces</td></tr>
		<tr><td>b</td><td>short</td></tr>
	</table>`
	md, err := newConverter().ConvertString(input, converter.WithDomain("https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "| Key | Description |") || !strings.Contains(md, "| b | short |") {
		t.Fatalf("expected unpadded Markdown table, got:\n%s", md)
	}
	if strings.Contains(md, "   ") {
		t.Fatalf("unexpected column padding spaces in:\n%s", md)
	}
}

func TestLayoutTablesUnwrapToPlainText(t *testing.T) {
	hn := `<table id="hnmain" border="0" cellpadding="0" cellspacing="0" width="85%">
		<tr><td><table border="0" cellpadding="0" cellspacing="0">
			<tr><td>1.</td><td><a href="https://example.com/post">Show HN: Huntsman</a> (example.com)</td></tr>
			<tr><td></td><td>142 points by alice | <a href="item?id=1">48 comments</a></td></tr>
		</table></td></tr>
	</table>`
	md, err := newConverter().ConvertString(hn, converter.WithDomain("https://news.ycombinator.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "|---") || strings.Contains(md, "| -") {
		t.Fatalf("layout table was rendered as Markdown table:\n%s", md)
	}
	for _, want := range []string{"[Show HN: Huntsman](https://example.com/post)", "142 points by alice", "https://news.ycombinator.com/item?id=1"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q in:\n%s", want, md)
		}
	}
	spaces := strings.Count(md, " ")
	if float64(spaces)/float64(len(md)) > 0.25 {
		t.Fatalf("too many spaces (%d/%d) in:\n%s", spaces, len(md), md)
	}
}

func TestPresentationTableUnwraps(t *testing.T) {
	input := `<table role="presentation"><tr><th>Left</th><th>Right</th></tr><tr><td>One</td><td>Two</td></tr></table>`
	md, err := newConverter().ConvertString(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "| -") || strings.Contains(md, "|---") {
		t.Fatalf("presentation table was rendered as Markdown table:\n%s", md)
	}
	if !strings.Contains(md, "One") || !strings.Contains(md, "Two") {
		t.Fatalf("presentation table text lost:\n%s", md)
	}
}
