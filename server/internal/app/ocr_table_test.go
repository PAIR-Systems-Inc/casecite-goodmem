package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoodMemOCRHTMLTables(t *testing.T) {
	for _, tc := range []struct{ name, html, want string }{
		{"entities and punctuation", `<table><tr><th>Party</th><th>Options</th></tr><tr><td>Acme &amp; Co.</td><td>A|B and C\D</td></tr></table>`, "| Party | Options |\n| --- | --- |\n| Acme & Co. | A\\|B and C\\\\D |"},
		{"merged header", `<table><tbody><tr><th colspan="2">Notice terms</th></tr><tr><td>Notice</td><td>30 days</td></tr></tbody></table>`, "| Notice terms |  |\n| --- | --- |\n| Notice | 30 days |"},
		{"merged rows and line breaks", `<table><tr><th>Term</th><th>Value</th></tr><tr><td rowspan="2">Timing</td><td>30 days<br>No penalty</td></tr><tr><td>60 days</td></tr></table>`, "| Term | Value |\n| --- | --- |\n| Timing | 30 days No penalty |\n|  | 60 days |"},
		{"caption and formatting", `<table><caption>Payment schedule</caption><tr><th>Term</th><th>Value</th></tr><tr><td><strong>Fee</strong></td><td>$12,345.67</td></tr></table>`, "Payment schedule\n\n| Term | Value |\n| --- | --- |\n| Fee | $12,345.67 |"},
		{"empty cell", `<table><tr><td>Term</td><td>Value</td></tr><tr><td>Optional</td><td></td></tr></table>`, "| Term | Value |\n| --- | --- |\n| Optional |  |"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeOCRTable(tc.html); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
	for _, raw := range []string{
		"| Term | Value |\n| --- | --- |\n| Notice | 30 days |",
		`<table><tr><td colspan="999999">Amount</td></tr></table>`,
		`<table><tr><td>Unclosed table`,
		`<table>Unexpected text<tr><td>Value</td></tr></table>`,
		`<table><tr><td>x<y</td></tr></table>`,
		`<table><tr><td><table><tr><td>Nested</td></tr></table></td></tr></table>`,
	} {
		if got := normalizeOCRTable(raw); got != raw {
			t.Fatalf("unsupported markup must retain all original data: %q -> %q", raw, got)
		}
	}
}

func TestGoodMemOCRTableOffsetsUseNormalizedText(t *testing.T) {
	const raw = `<table><tr><th>Term</th><th>Value</th></tr><tr><td>Notice</td><td>30 days</td></tr></table>`
	const md = "| Term | Value |\n| --- | --- |\n| Notice | 30 days |"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, M{"pageCount": 2, "pages": []M{
			{"pageIndex": 0, "page": M{"layout": M{"cells": []M{{"text": "📜 Schedule"}, {"text": raw, "category": "OCR_CATEGORY_TABLE"}}}, "markdown": "Do not substitute a different page-level rendering"}},
			ocrPage(1, "Second page."),
		}})
	}))
	defer provider.Close()
	g := NewGoodMem(Config{GoodMemURL: provider.URL})
	out, err := g.OCR(context.Background(), []byte("fixture"), "PDF", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Complete || out.Pages[0].Text != "📜 Schedule\n"+md || len(out.Pages[0].Tables) != 1 || out.Pages[0].Tables[0] != md {
		t.Fatal(out)
	}
	if out.Pages[1].Start != byteToUTF16(out.Pages[0].Text, len(out.Pages[0].Text))+2 || strings.Contains(out.Text, "<td>") {
		t.Fatal("offsets or table text diverged", out)
	}
}
