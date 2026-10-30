package app

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type pdfTableFixture struct {
	Name     string   `json:"name"`
	Content  []byte   `json:"content"`
	Tables   []string `json:"tables"`
	Text     string   `json:"text"`
	NeedsOCR bool     `json:"needs_ocr"`
}

func pdfTableFixtures(t *testing.T) []pdfTableFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/pdf_tables.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Baseline string            `json:"baseline"`
		Cases    []pdfTableFixture `json:"cases"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" || len(oracle.Cases) != 12 {
		t.Fatal("unreviewed PDF baseline")
	}
	return oracle.Cases
}
func expectedPDFTables(c pdfTableFixture) []string {
	out := append([]string(nil), c.Tables...)
	if c.Name == "literal-punctuation" {
		out[0] = strings.ReplaceAll(out[0], `A|B and C\D`, `A\|B and C\\D`)
	}
	return out
}
func TestNativePDFTablesMatchOriginalCells(t *testing.T) {
	for _, fixture := range pdfTableFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			pages, err := nativePDFPages(fixture.Content)
			if err != nil || len(pages) != 1 {
				t.Fatal(pages, err)
			}
			page := pages[0]
			if fixture.NeedsOCR {
				if !page.Failed || page.Method != "goodmem_ocr" || page.Text != "" {
					t.Fatal("unsupported layout silently accepted partial text", page)
				}
				return
			}
			if page.Failed || page.Method != "native" {
				t.Fatal("readable table unnecessarily requires OCR", page)
			}
			legalEqual(t, page.Tables, expectedPDFTables(fixture))
			if strings.Count(page.Text, "Agreement schedule:") != 1 || strings.Count(page.Text, "End of schedule.") != 1 {
				t.Fatal("surrounding text lost or duplicated", page.Text)
			}
			if len(page.Tables) > 0 {
				start, end := strings.Index(page.Text, page.Tables[0]), strings.LastIndex(page.Text, page.Tables[len(page.Tables)-1])
				if start < strings.Index(page.Text, "Agreement schedule:") || end > strings.Index(page.Text, "End of schedule.") {
					t.Fatal("table not in physical reading order", page.Text)
				}
				if fixture.Name == "two-tables" {
					mid := strings.Index(page.Text, "Between tables:")
					if mid < start || mid > end {
						t.Fatal("intervening paragraph moved out of order", page.Text)
					}
				}
			}
		})
	}
}

func TestPDFTablesSharedReadersAndGoodMemFallback(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	var ocrCalls atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		ocrCalls.Add(1)
		writeJSON(w, 200, M{"pageCount": 1, "pages": []M{{"pageIndex": 0, "page": M{"layout": M{"cells": []M{{"text": "Complete schedule from the GoodMem OCR service."}, {"text": "| Term | Value |\n| --- | --- |\n| Notice | 30 days |", "category": "TABLE"}}}}}}})
		return true
	})
	for _, fixture := range pdfTableFixtures(t) {
		raw, _ := f.fileTool("extract-text", token, fixture.Name+".pdf", fixture.Content, 200)
		var out M
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out["ocr_used"] != fixture.NeedsOCR || number(out, "tables_found", -1) != len(fixture.Tables) {
			t.Fatal(fixture.Name, out)
		}
		if !fixture.NeedsOCR && len(fixture.Tables) > 0 && !strings.Contains(str(out, "text"), expectedPDFTables(fixture)[0]) {
			t.Fatal(out)
		}
		if fixture.Name == "ruled" {
			doc := f.upload(token, "", "schedule.pdf", string(fixture.Content))
			if str(doc, "ingest_job_id") != "" {
				t.Fatal("native table queued paid OCR", doc)
			}
			text, _, err := f.s.documentText(context.Background(), user, str(doc, "id"))
			if err != nil || !strings.Contains(text, expectedPDFTables(fixture)[0]) {
				t.Fatal(text, err)
			}
		}
	}
	if ocrCalls.Load() != 2 {
		t.Fatal("only form and rotated pages should use OCR", ocrCalls.Load())
	}
}

func TestPDFTableGeometryLimits(t *testing.T) {
	lines := []pdfRule{}
	for i := 0; i < 513; i++ {
		lines = append(lines, pdfRule{axis: float64(i * 10), lo: 0, hi: 10000})
	}
	if _, _, _, err := pdfGridCells(lines, lines); err == nil {
		t.Fatal("excessive table grid accepted")
	}
}

func TestPDFTextLimitDoesNotSpendOCRWork(t *testing.T) {
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	_, _ = z.Write([]byte("BT /F1 12 Tf 50 700 Td (" + strings.Repeat("A", maxExtractedText+1) + ") Tj ET"))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	objects := []string{`<< /Type /Catalog /Pages 2 0 R >>`, `<< /Type /Pages /Kids [3 0 R] /Count 1 >>`, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`, `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`, fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", compressed.Len(), compressed.Bytes())}
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := []int{}
	for i, object := range objects {
		offsets = append(offsets, pdf.Len())
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	f := newFixture(t)
	token := f.admin()
	var calls atomic.Int32
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		calls.Add(1)
		w.WriteHeader(500)
		return true
	})
	raw, _ := f.fileTool("extract-text", token, "text-limit.pdf", pdf.Bytes(), 422)
	if !strings.Contains(string(raw), "4 MB") {
		t.Fatal(string(raw))
	}
	doc := f.upload(token, "", "text-limit.pdf", pdf.String())
	out := f.call("GET", "/documents/"+str(doc, "id")+"/content", token, nil, 422)
	if !strings.Contains(str(out, "detail"), "4 MB") || calls.Load() != 0 {
		t.Fatal(out, calls.Load())
	}
}
