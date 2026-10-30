package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func ocrPage(index int, text string) M {
	return M{"pageIndex": index, "page": M{"layout": M{"cells": []M{{"text": text}}}, "markdown": "synthetic markdown must not replace the layout"}}
}

func ocrFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoodMemOCRContractAndPageSpans(t *testing.T) {
	content := ocrFile(t, "mixed_notice.pdf")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Content  []byte `json:"content"`
			Format   string `json:"format"`
			Start    int    `json:"startPage"`
			End      int    `json:"endPage"`
			Markdown bool   `json:"includeMarkdown"`
			Raw      bool   `json:"includeRawJson"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if r.Method != "POST" || r.URL.Path != "/v1/ocr:document" || r.Header.Get("x-api-key") != "ocr-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect OCR request", r.Method, r.URL, r.Header)
		}
		if !bytes.Equal(content, input.Content) || input.Format != "PDF" || input.Start != 1 || input.End != 2 || !input.Markdown || input.Raw {
			t.Error("incorrect OCR body/range")
		}
		writeJSON(w, 200, M{"pageCount": 2, "pages": []M{ocrPage(2, "Second page."), ocrPage(1, "📜 Notice is 30 days.")}})
	}))
	defer provider.Close()
	g := NewGoodMem(Config{GoodMemURL: provider.URL, GoodMemKey: "ocr-key"})
	start, end := 1, 2
	result, err := g.OCR(context.Background(), content, "PDF", &start, &end)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "📜 Notice is 30 days.\n\nSecond page." || result.Method != "goodmem_ocr" || !result.Complete || len(result.FailedPages) != 0 {
		t.Fatal(result)
	}
	first := result.Pages[0]
	if first.Index != 1 || first.Start != 0 || first.End != 21 || result.Pages[1].Start != 23 || result.Pages[1].End != 35 {
		t.Fatal(result.Pages)
	}
	quote := M{"quote": "Notice is 30 days."}
	verifyQuote(quote, SourceDocument{"d", "scan.pdf", result.Text, "full text (GoodMem OCR)"})
	if quote["quote_verified"] != true || quote["span_start"] != 3 {
		t.Fatal(quote)
	}
}

func TestGoodMemOCRRejectsInvalidCoverage(t *testing.T) {
	for _, tc := range []struct {
		name             string
		body             M
		invalid, partial bool
	}{
		{"duplicate", M{"pageCount": 2, "pages": []M{ocrPage(0, "first"), ocrPage(0, "duplicate")}}, true, false},
		{"negative", M{"pageCount": 1, "pages": []M{ocrPage(-1, "wrong")}}, true, false},
		{"out of range", M{"pageCount": 1, "pages": []M{ocrPage(1, "wrong")}}, true, false},
		{"missing index", M{"pageCount": 1, "pages": []M{{"page": M{"markdown": "wrong"}}}}, true, false},
		{"empty count", M{"pageCount": 0, "pages": []M{}}, true, false},
		{"missing page", M{"pageCount": 2, "pages": []M{ocrPage(0, "first")}}, false, true},
		{"failed page", M{"pageCount": 2, "pages": []M{ocrPage(0, "first"), {"pageIndex": 1, "status": M{"code": 13}}}}, false, true},
		{"empty result", M{"pageCount": 1, "pages": []M{{"pageIndex": 0, "page": M{}}}}, false, true},
		{"markdown only", M{"pageCount": 1, "pages": []M{{"pageIndex": 0, "page": M{"markdown": "Only Markdown."}}}}, false, false},
		{"empty layout with markdown", M{"pageCount": 1, "pages": []M{{"pageIndex": 0, "page": M{"layout": M{"cells": []M{}}, "markdown": "Only Markdown."}}}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, tc.body) }))
			defer provider.Close()
			g := NewGoodMem(Config{GoodMemURL: provider.URL})
			result, err := g.OCR(context.Background(), []byte("image"), "AUTO", nil, nil)
			if (err != nil) != tc.invalid {
				t.Fatal(result, err)
			}
			if err == nil && result.Complete == tc.partial {
				t.Fatal(result)
			}
			if tc.partial && result.fullTextError() == nil {
				t.Fatal("partial OCR accepted as full text")
			}
			if strings.Contains(tc.name, "markdown") && result.Text != "Only Markdown." {
				t.Fatal(result)
			}
		})
	}
	blank := assembleText([]textPage{{Index: 0, Method: "goodmem_ocr"}})
	if !blank.Complete || blank.fullTextError() == nil {
		t.Fatal("blank document must not become readable text", blank)
	}
}

func TestGoodMemOCRErrorsDoNotRetryOrRedirect(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 413, 422, 429, 500, 503, 307} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(status)
			}))
			defer provider.Close()
			g := NewGoodMem(Config{GoodMemURL: provider.URL})
			_, err := g.OCR(context.Background(), []byte("scan"), "PNG", nil, nil)
			want := 502
			if status == 401 || status == 403 || status == 404 {
				want = 503
			}
			if status == 400 || status == 413 || status == 422 {
				want = 422
			}
			var api *apiError
			if !errors.As(err, &api) || api.status != want || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
}

func TestNativePDFScanDetection(t *testing.T) {
	pages, err := nativePDFPages(ocrFile(t, "mixed_notice.pdf"))
	if err != nil || len(pages) != 3 {
		t.Fatal(pages, err)
	}
	if pages[0].Failed || !strings.Contains(pages[0].Text, "Native first page.") || !pages[1].Failed || !pages[2].Failed {
		t.Fatal(pages)
	}
	pages, err = nativePDFPages(ocrFile(t, "scanned_notice.pdf"))
	if err != nil || len(pages) != 1 || !pages[0].Failed || pages[0].Text != "" {
		t.Fatal(pages, err)
	}
	pages, err = nativePDFPages(samplePDF())
	if err != nil || !pages[0].Failed {
		t.Fatal("sparse-page OCR threshold must match original", pages, err)
	}
	if ocrInputFormat(ocrFile(t, "scanned_notice.png")) != "PNG" || ocrInputFormat(ocrFile(t, "scanned_notice.pdf")) != "PDF" || ocrInputFormat([]byte{'I', 'I', 42, 0}) != "TIFF" {
		t.Fatal("format detection")
	}
}
