package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func samplePDF() []byte {
	stream := "BT /F1 14 Tf 60 740 Td (Notice is 30 days.) Tj 0 -24 Td (Payment is due monthly.) Tj ET"
	objects := []string{`<< /Type /Catalog /Pages 2 0 R >>`, `<< /Type /Pages /Kids [3 0 R] /Count 1 >>`, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`, `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, o := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, n := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", n)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}
func TestPDFText(t *testing.T) {
	text, err := extractText(samplePDF(), "notice.pdf")
	if err != nil || !strings.Contains(text, "Notice is 30 days.") {
		t.Fatal(text, err)
	}
}

// Opt-in test against an isolated, actual GoodMem server. The configured space
// must use a working embedder. Every memory created here is deleted on exit.
func TestRealGoodMemDocumentLifecycle(t *testing.T) {
	file := os.Getenv("TEST_GOODMEM_CONFIG")
	if file == "" {
		t.Skip("set TEST_GOODMEM_CONFIG to exercise the real GoodMem API")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m := M{}
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	g := NewGoodMem(Config{GoodMemURL: str(m, "base_url"), GoodMemKey: str(m, "api_key"), GoodMemSpace: str(m, "space_id")})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id := uuid()
	pdf := samplePDF()
	if _, err = g.Create(ctx, id, uuid(), "notice.pdf", "application/pdf", pdf); err != nil {
		t.Fatal("create", err)
	}
	defer g.Delete(context.Background(), id)
	if _, err = g.Wait(ctx, id); err != nil {
		t.Fatal("process", err)
	}
	original, err := g.Content(ctx, id)
	if err != nil || !bytes.Equal(original, pdf) {
		t.Fatal("original content not retained", err)
	}
	pages, err := g.Pages(ctx, id)
	if err != nil || len(pages) != 1 {
		t.Fatal("page metadata", pages, err)
	}
	page, err := g.Page(ctx, id, "0", url.Values{})
	if err != nil {
		t.Fatal("page image", err)
	}
	data, err := io.ReadAll(page.Body)
	page.Body.Close()
	if err != nil || len(data) < 100 {
		t.Fatal("invalid image", err)
	}
	if !strings.HasPrefix(page.Header.Get("Content-Type"), "image/") {
		t.Fatal(page.Header)
	}
	hits, err := g.Search(ctx, "notice period", []string{id}, 8)
	if err != nil || len(hits) == 0 {
		t.Fatal("retrieval", hits, err)
	}
	for _, h := range hits {
		if h.MemoryID != id || !strings.Contains(h.Text, "30 days") {
			t.Fatal("incorrect retrieved evidence", h)
		}
	}
	if err = g.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Memory(ctx, id); err == nil {
		t.Fatal("deleted memory still accessible")
	}
	if err = g.Delete(ctx, id); err != nil {
		t.Fatal("delete retry not idempotent", err)
	}
	if fixture := os.Getenv("TEST_PDF_OUTPUT"); fixture != "" {
		if err = os.WriteFile(fixture, pdf, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
