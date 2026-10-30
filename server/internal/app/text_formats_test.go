package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type extractionFixture struct {
	Name          string `json:"name"`
	Content       []byte `json:"content"`
	Text          string `json:"text"`
	Tables, Pages int
	Error         bool
	Improved      *string `json:"improved_text"`
	Improvement   string
}

func extractionFixtures(t *testing.T) []extractionFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/text_extraction.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Baseline string
		Cases    []extractionFixture
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if fixtures.Baseline != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal("unrecognized baseline")
	}
	return fixtures.Cases
}
func TestDocumentExtractionPythonOracle(t *testing.T) {
	for _, fixture := range extractionFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			out, err := extractNativeDocument(fixture.Content, fixture.Name)
			if fixture.Error && fixture.Improved == nil {
				if err == nil && strings.TrimSpace(out.Text) != "" {
					t.Fatalf("unreadable document was accepted: %q", out.Text)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := fixture.Text
			if fixture.Improved != nil {
				want = *fixture.Improved
				if fixture.Improvement == "" {
					t.Fatal("difference has no documented reason")
				}
			}
			if out.Text != want {
				t.Fatalf("text differs\ngot:  %q\nwant: %q", out.Text, want)
			}
			if len(out.Tables) != fixture.Tables || len(out.Pages) != fixture.Pages {
				t.Fatalf("metadata tables=%d pages=%d, want tables=%d pages=%d", len(out.Tables), len(out.Pages), fixture.Tables, fixture.Pages)
			}
			text, err := extractText(fixture.Content, fixture.Name)
			if err != nil || (fixture.Name != "agreement.docx" && text != out.Text) {
				t.Fatalf("full-document path differs: %q %v", text, err)
			}
			if _, err = validateDocumentUpload(fixture.Content, fixture.Name); err != nil {
				t.Fatalf("native reader works but upload was rejected: %v", err)
			}
		})
	}
}

func documentFixtureArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, text := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestDocumentReaderMalformedInputAndQuality(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"utf16.txt", []byte{0xff, 0xfe, 65}},
		{"truncated.rtf", []byte(`{\rtf1 Text {\b broken}`)},
		{"binary.rtf", []byte(`{\rtf1\bin1000 short}`)},
		{"huge.rtf", []byte(`{\rtf1\uc9999999999999999999999999 x}`)},
		{"nested.rtf", []byte(`{\rtf1 ` + strings.Repeat("{", 300) + "x" + strings.Repeat("}", 301))},
		{"mismatch.odt", documentFixtureArchive(t, map[string]string{"content.xml": `<x><y></x>`})},
		{"entity.odt", documentFixtureArchive(t, map[string]string{"content.xml": `<!DOCTYPE x [<!ENTITY x SYSTEM "file:///etc/passwd">]><x>&x;</x>`})},
		{"nested.docx", documentFixtureArchive(t, map[string]string{"word/document.xml": strings.Repeat("<x>", 300) + strings.Repeat("</x>", 300)})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractNativeDocument(tc.data, tc.name); err == nil {
				t.Fatal("malformed document accepted")
			}
		})
	}
	word := []byte("small")
	plc := make([]byte, 16)
	binary.LittleEndian.PutUint32(plc[4:], 1000)
	binary.LittleEndian.PutUint32(plc[10:], 0x40000000)
	clx := append([]byte{2, 16, 0, 0, 0}, plc...)
	if _, err := wordPieceText(word, clx, 1000); err == nil {
		t.Fatal("truncated piece silently accepted")
	}
	rtf := []byte(`{\rtf1\ansi Before {\*\unknown hidden}{\pict 1234} after.\par Emoji \u-10179?\u-8704?}`)
	if text, err := rtfText(rtf); err != nil || text != "Before  after.\nEmoji 😀" {
		t.Fatalf("RTF destinations/unicode: %q %v", text, err)
	}
	odt := documentFixtureArchive(t, map[string]string{"content.xml": `<o xmlns:t="urn:oasis:names:tc:opendocument:xmlns:text:1.0"><t:p>A<t:s t:c="3"/>B<t:tab/>C<t:line-break/>D</t:p></o>`})
	if out, err := extractNativeDocument(odt, "spaces.odt"); err != nil || out.Text != "A   B\tC\nD" {
		t.Fatalf("ODT spacing: %q %v", out.Text, err)
	}
	if _, err := validateDocumentUpload(make([]byte, (10<<20)+1), "too-big.txt"); err == nil {
		t.Fatal("text type size cap ignored")
	}
}

func (f *fixture) fileTool(path, token, name string, data []byte, want int) ([]byte, http.Header) {
	f.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	p, _ := w.CreateFormFile("file", name)
	p.Write(data)
	w.Close()
	r, _ := http.NewRequest("POST", f.http.URL+"/api/v1/legal-docs/"+path, &b)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != want {
		f.t.Fatalf("%s %s: got %d want %d: %s", path, name, resp.StatusCode, want, raw)
	}
	return raw, resp.Header
}
func TestLegalDocumentExtractionAndPreview(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	user := f.account(admin, "file-tools@example.test")
	f.call("GET", "/legal-docs/courts", "expired", nil, 401)
	courts := f.call("GET", "/legal-docs/courts", user, nil, 200)
	if number(courts, "count", 0) != 41 {
		t.Fatalf("court list: %v", courts)
	}
	for _, fixture := range extractionFixtures(t) {
		if fixture.Error && fixture.Improved == nil {
			continue
		}
		raw, _ := f.fileTool("extract-text", user, fixture.Name, fixture.Content, 200)
		out := M{}
		json.Unmarshal(raw, &out)
		want := fixture.Text
		if fixture.Improved != nil {
			want = *fixture.Improved
		}
		if str(out, "text") != want || out["success"] != true || out["ocr_used"] != false || out["truncated"] != false {
			t.Fatalf("%s extraction response: %v", fixture.Name, out)
		}
		if warnings, ok := out["warnings"].([]any); !ok || len(warnings) != 0 {
			t.Fatal("unexpected warnings")
		}
		if fixture.Name == "agreement.docx" {
			preview, _ := f.fileTool("convert-to-pdf", user, fixture.Name, fixture.Content, 200)
			if !bytes.HasPrefix(preview, []byte("%PDF-")) {
				t.Fatalf("Word preview missing PDF bytes: %d", len(preview))
			}
			text, e := extractText(preview, "preview.pdf")
			if e != nil || !strings.Contains(text, "30 days") {
				t.Fatalf("Word table missing from preview: %q %v", text, e)
			}
			if directory := os.Getenv("PARITY_ARTIFACT_DIR"); directory != "" {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "word-preview.pdf"), preview, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, tc := range []struct {
		name, text string
		status     int
	}{{"binary.exe", "MZ", 415}, {"empty.txt", "", 422}, {"bad.doc", "not word", 415}, {"bad.txt", strings.Repeat("x", (1<<20)+1), 413}} {
		f.fileTool("extract-text", user, tc.name, []byte(tc.text), tc.status)
	}
	f.fileTool("extract-text", "expired", "note.txt", []byte("note"), 401)
	pdf := []byte("%PDF-1.4\n%%EOF\n")
	raw, header := f.fileTool("convert-to-pdf", user, "input.pdf", pdf, 200)
	if !bytes.Equal(raw, pdf) || header.Get("Content-Type") != "application/pdf" {
		t.Fatal("PDF pass-through changed")
	}
	raw, header = f.fileTool("convert-to-pdf", user, "notice.txt", []byte("Payment is due in thirty days.\nConfirm receipt."), 200)
	if !bytes.HasPrefix(raw, []byte("%PDF-")) || header.Get("Content-Disposition") != `inline; filename="document.pdf"` {
		t.Fatal("PDF response headers/content")
	}
	text, err := extractText(raw, "preview.pdf")
	if err != nil || !strings.Contains(text, "Payment is due") {
		t.Fatalf("rendered content: %q %v", text, err)
	}
	docx, err := wordDocument("Agreement", "Payment is due in thirty days.")
	if err != nil {
		t.Fatal(err)
	}
	f.fileTool("convert-to-pdf", user, "agreement.docx", docx, 200)
	// Custom roles can remove file tools. Authentication alone is insufficient.
	role := stringsOf(f.call("GET", "/auth/me", user, nil, 200)["roles"])[0]
	f.call("PUT", "/admin/users/roles/"+role, admin, M{"permissions": []string{"chat.use"}}, 200)
	f.fileTool("extract-text", user, "note.txt", []byte("Payment due"), 403)
	f.fileTool("convert-to-pdf", user, "note.txt", []byte("Payment due"), 403)
	f.call("GET", "/legal-docs/courts", user, nil, 200)
}

func TestUploadedFileUsesGoodMemOCRWithoutCreatingDocuments(t *testing.T) {
	f := newFixture(t)
	access := f.admin()
	var calls atomic.Int32
	var partial atomic.Bool
	var blank atomic.Bool
	routeOCR(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v1/ocr:document" {
			return false
		}
		calls.Add(1)
		var input M
		json.NewDecoder(r.Body).Decode(&input)
		if input["format"] != "PDF" || input["includeMarkdown"] != true || number(input, "startPage", -1) != 1 || number(input, "endPage", -1) != 2 {
			t.Error("unexpected OCR request", input["format"], input["startPage"], input["endPage"])
		}
		if blank.Load() {
			writeJSON(w, 200, M{"pageCount": 2, "pages": []M{ocrPage(1, ""), ocrPage(2, "")}})
			return true
		}
		second := ocrPage(2, "Notice is 30 days.")
		if partial.Load() {
			second = M{"pageIndex": 2, "status": M{"code": 13}}
		}
		writeJSON(w, 200, M{"pageCount": 2, "pages": []M{{"pageIndex": 1, "page": M{"layout": M{"cells": []M{{"text": "| Party | Notice |\n| --- | --- |\n| Acme | 30 days |", "category": "TABLE"}}}}}, second}})
		return true
	})
	raw, _ := f.fileTool("extract-text", access, "mixed_notice.pdf", ocrFile(t, "mixed_notice.pdf"), 200)
	out := M{}
	json.Unmarshal(raw, &out)
	if out["ocr_used"] != true || number(out, "page_count", 0) != 3 || number(out, "tables_found", 0) != 1 || !strings.Contains(str(out, "text"), "Acme") {
		t.Fatalf("OCR extraction response: %v", out)
	}
	partial.Store(true)
	raw, _ = f.fileTool("extract-text", access, "mixed_notice.pdf", ocrFile(t, "mixed_notice.pdf"), 502)
	if !strings.Contains(string(raw), "pages 3") {
		t.Fatalf("partial OCR must be visible: %s", raw)
	}
	blank.Store(true)
	raw, _ = f.fileTool("extract-text", access, "mixed_notice.pdf", ocrFile(t, "mixed_notice.pdf"), 200)
	json.Unmarshal(raw, &out)
	if out["ocr_used"] != true || number(out, "page_count", 0) != 3 || strings.TrimSpace(str(out, "text")) == "" {
		t.Fatal("blank scanned pages discarded native content", out)
	}
	if calls.Load() != 3 {
		t.Fatal("unexpected OCR retries", calls.Load())
	}
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE kind='document'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("temporary extraction created permanent documents", count, err)
	}
}
