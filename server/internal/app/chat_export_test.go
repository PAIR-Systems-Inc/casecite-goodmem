package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pdfreader "github.com/ledongthuc/pdf"
)

func TestConversationExportOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/conversation_export.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := M{}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for i, tc := range maps(fixture["cases"]) {
		var actual any
		switch tc["kind"] {
		case "inline":
			actual = conversationInline(str(tc, "input"))
		case "filename":
			actual = conversationFilename(str(tc, "input"), "docx")
		case "markdown":
			actual = conversationMarkdown(object(tc["input"]), "2026-10-29 19:00 UTC")
		case "blocks":
			rows := [][]string{}
			for _, b := range conversationBlocks(str(tc, "input")) {
				kind := map[string]string{"text": "para", "subheading": "heading", "unordered": "bullet", "ordered": "number"}[b.Kind]
				rows = append(rows, []string{kind, b.Text})
			}
			actual = rows
		}
		if string(jsonBytes(actual)) != string(jsonBytes(tc["expected"])) {
			t.Fatalf("export %d (%s): got %s want %s", i, tc["kind"], jsonBytes(actual), jsonBytes(tc["expected"]))
		}
	}
}

func conversationExportExample() M {
	answer := "## Notice requirements\nThe agreement requires **30 days** of notice after an uncured material breach.\n\n- Preserve the notice and delivery record.\n- Check the cure period.\n\n## Next steps\n1. Confirm the date of receipt.\n2. Review the amended agreement.\n\nCafé résumé — § 12. Ελληνικά Пример.\nLiteral text: <notice> & no executable markup.\n\n"
	answer += strings.Repeat("The review covers the quoted notice provision. An amendment may change the applicable period. Confirm the complete document before acting.\n", 45)
	return M{"title": "Notice review conversation", "format": "docx", "messages": []M{
		{"role": "user", "text": "What notice does the agreement require?", "citations": []M{}},
		{"role": "assistant", "text": answer, "citations": []M{{"label": "Agreement § 12", "quote": "Either party may terminate on 30 days notice, only after an uncured material breach.", "url": "https://example.test/agreement/" + strings.Repeat("long-source-path-", 14)}}},
		{"role": "user", "text": "What should we verify next?", "citations": []M{}},
		{"role": "assistant", "text": "1. Verify the amendment.\n2. Preserve the proof of service.", "citations": []M{}},
	}}
}

func TestConversationWordAndPDFPreserveStructureAndUnicode(t *testing.T) {
	now := time.Date(2026, 10, 29, 19, 0, 0, 0, time.UTC)
	in := conversationExportExample()
	docx, err := renderConversation(context.Background(), in, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := zipParts(t, docx)
	for name, raw := range parts {
		decoder := xml.NewDecoder(strings.NewReader(raw))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	visible := revisionView(t, parts["word/document.xml"], true)
	for _, text := range []string{"Notice review conversation", "Notice requirements", "Agreement § 12", "Literal text: <notice> & no executable markup.", "Café résumé", "Ελληνικά Пример", aiReviewNotice} {
		if !strings.Contains(visible, text) {
			t.Fatalf("Word omitted %q", text)
		}
	}
	if strings.Count(parts["word/numbering.xml"], `<w:num w:numId=`) != 3 || !strings.Contains(parts["word/document.xml"], `<w:numPr>`) {
		t.Fatal("Word lost native lists or independent list restarts")
	}
	if !strings.Contains(parts["word/styles.xml"], `w:styleId="Title"`) || strings.Contains(parts["word/styles.xml"], `203E56`) {
		t.Fatal("Word title/heading styles incorrect")
	}
	in["format"] = "pdf"
	pdf, err := renderConversation(context.Background(), in, now)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := pdfreader.NewReader(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	if reader.NumPage() < 2 {
		t.Fatal("long transcript did not paginate")
	}
	textReader, err := reader.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(textReader)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Notice review conversation", "Café résumé", "Ελληνικά Пример", "AI-generated draft.", "Verify the amendment."} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("PDF omitted %q: %s", want, text)
		}
	}
	if dir := os.Getenv("PARITY_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"chat-transcript.docx": docx, "chat-transcript.pdf": pdf} {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestConversationPDFDoesNotSilentlyDropUnsupportedGlyphs(t *testing.T) {
	in := M{"title": "Conversation", "format": "pdf", "messages": []M{{"role": "user", "text": "漢字"}}}
	_, err := renderConversation(context.Background(), in, time.Now())
	api, ok := err.(*apiError)
	if !ok || api.status != 422 || !strings.Contains(api.message, "Word or Markdown") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := renderConversation(ctx, conversationExportExamplePDF(), time.Now()); err != context.Canceled {
		t.Fatal(err)
	}
}

func conversationExportExamplePDF() M {
	in := conversationExportExample()
	in["format"] = "pdf"
	return in
}

func TestConversationExportEndpointUsesProvidedTranscript(t *testing.T) {
	f := chatFixture(t, func(M, string, string) { t.Error("export called a model") })
	token := f.admin()
	for _, format := range []string{"md", "docx", "pdf"} {
		in := M{"title": "Notice \"review\"\r\n", "format": format, "messages": []M{{"role": "user", "text": "Question?"}, {"role": "assistant", "text": "30 days, subject to a material breach.", "citations": []M{{"label": "Agreement", "quote": "30 days, subject to a material breach."}}}}}
		request, _ := http.NewRequest("POST", f.http.URL+"/api/v1/chat/export", bytes.NewReader(jsonBytes(in)))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || len(data) == 0 || !strings.Contains(response.Header.Get("Content-Disposition"), "Notice-review."+format) {
			t.Fatal(response.StatusCode, response.Header, err, string(data))
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.retrieved) != 0 {
		t.Fatal("export queried GoodMem")
	}
}
