package app

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"testing"
)

func zipFixture(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, part := range parts {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRedlinesPreserveStrictNamespaceAndControlTokens(t *testing.T) {
	// "w" deliberately belongs to DrawingML; generated changes must not
	// rebind it around original runs. Strict Word uses a separate prefix.
	base := `<x:document xmlns:x="` + strictWordNamespace + `" xmlns:w="http://schemas.openxmlformats.org/drawingml/2006/main"><x:body><x:p><x:r><x:rPr><x:b/></x:rPr><x:t>Invoice</x:t><x:tab/><x:t>is due</x:t><x:br x:type="textWrapping"/><x:t>within 30 days.</x:t><x:drawing><w:t>image label</w:t></x:drawing></x:r></x:p><x:sectPr/></x:body></x:document>`
	result, err := redlineDOCX(zipFixture(t, map[string]string{"word/document.xml": base}), []M{{"ref": "timing", "original_text": "Invoice is due within 30 days.", "proposed_text": "Invoice is due within 45 days.", "span_start": 0}}, "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Skipped) != 0 {
		t.Fatal(result.Applied, result.Skipped)
	}
	raw := zipParts(t, result.Content)["word/document.xml"]
	ids := map[string]bool{}
	d := xml.NewDecoder(strings.NewReader(raw))
	controls, drawingLabels := 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		e, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if e.Name.Space == wordNamespace {
			t.Fatalf("strict OOXML mixed with transitional: %s", e.Name.Local)
		}
		if e.Name.Local == "t" && e.Name.Space == "http://schemas.openxmlformats.org/drawingml/2006/main" {
			drawingLabels++
		}
		if e.Name.Space != strictWordNamespace {
			continue
		}
		if e.Name.Local == "tab" || e.Name.Local == "br" {
			controls++
		}
		if e.Name.Local != "del" && e.Name.Local != "ins" {
			continue
		}
		for _, a := range e.Attr {
			if a.Name.Local == "id" {
				if ids[a.Value] {
					t.Fatalf("duplicate revision id %s", a.Value)
				}
				ids[a.Value] = true
			}
		}
	}
	if len(ids) != 6 || controls != 2 || drawingLabels != 1 {
		t.Fatalf("lost revision/control/namespace: %d %d %d", len(ids), controls, drawingLabels)
	}
	root, err := readWordXML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if actual := wordVisibleText(root, ids, 0); actual != "Invoice\tis due\nwithin 30 days.\n" {
		t.Fatalf("rejected text %q", actual)
	}
	if actual := wordVisibleText(root, nil, 0); actual != "Invoice is due within 45 days.\n" {
		t.Fatalf("accepted text %q", actual)
	}
}

func TestRedlinesPreserveExistingRevisions(t *testing.T) {
	base := `<w:document xmlns:w="` + wordNamespace + `"><w:body><w:p><w:del w:id="7" w:author="Previous"><w:r><w:delText>Old</w:delText></w:r></w:del><w:ins w:id="8" w:author="Previous"><w:r><w:rPr><w:i/></w:rPr><w:t>Current payment term.</w:t></w:r></w:ins></w:p></w:body></w:document>`
	result, err := redlineDOCX(zipFixture(t, map[string]string{"word/document.xml": base}), []M{{"original_text": "Current payment term.", "proposed_text": "Revised payment term."}}, "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Skipped) > 0 {
		t.Fatal(result)
	}
	raw := zipParts(t, result.Content)["word/document.xml"]
	if !strings.Contains(raw, `<w:del w:id="7" w:author="Previous"><w:r><w:delText>Old</w:delText></w:r></w:del>`) || !strings.Contains(raw, `<w:ins w:id="8" w:author="Previous">`) {
		t.Fatal("existing revisions changed")
	}
	root, err := readWordXML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	reject := map[string]bool{}
	walkWord(root, func(n *wordNode) {
		if !isWord(n, "ins") && !isWord(n, "del") {
			return
		}
		for _, a := range n.Attrs {
			if a.Name.Local == "id" {
				id, _ := strconv.Atoi(a.Value)
				if id > 8 {
					reject[a.Value] = true
				}
			}
		}
	})
	if got := wordVisibleText(root, reject, 0); got != "Current payment term.\n" {
		t.Fatalf("rejecting new edits changed earlier revisions: %q", got)
	}
	if got := wordVisibleText(root, nil, 0); got != "Revised payment term.\n" {
		t.Fatal(got)
	}
}

func TestRedlineExportNoticeAndUnappliedProposals(t *testing.T) {
	source, err := wordDocument("", "Source text.")
	if err != nil {
		t.Fatal(err)
	}
	result, err := redlineDOCX(source, []M{{"ref": "addition", "kind": "insert", "title": "New clause", "proposed_text": "First line.\nSecond line."}, {"ref": "missing", "original_text": "Absent passage.", "proposed_text": "The full unapplied replacement."}}, "Reviewer", true)
	if err != nil {
		t.Fatal(err)
	}
	actual := revisionView(t, zipParts(t, result.Content)["word/document.xml"], true)
	for _, want := range []string{"Proposed addition — New clause", "First line.\nSecond line.", "Absent passage.", "The full unapplied replacement.", aiReviewNotice} {
		if !strings.Contains(actual, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.HasSuffix(actual, aiReviewNotice+"\n") {
		t.Fatal("notice must be last")
	}
}
func zipParts(t *testing.T, b []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = string(raw)
	}
	return parts
}

// Independent reader models Word's accept/reject view using namespace-aware
// XML tokens, instead of reusing the patcher's tree or anchoring code.
func revisionView(t *testing.T, raw string, accept bool) string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(raw))
	var out strings.Builder
	skip, depth, textDepth := 0, 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			depth++
			if skip == 0 && ((accept && v.Name.Local == "del") || (!accept && v.Name.Local == "ins")) {
				skip = depth
			}
			if skip > 0 {
				continue
			}
			if v.Name.Local == "t" || v.Name.Local == "delText" {
				textDepth = depth
			}
			if v.Name.Local == "tab" {
				out.WriteByte('\t')
			}
			if v.Name.Local == "br" {
				out.WriteByte('\n')
			}
		case xml.EndElement:
			if skip == depth {
				skip = 0
			} else if skip == 0 && v.Name.Local == "p" {
				out.WriteByte('\n')
			}
			if textDepth == depth {
				textDepth = 0
			}
			depth--
		case xml.CharData:
			if skip == 0 && textDepth > 0 {
				out.Write(v)
			}
		}
	}
	return out.String()
}

func TestRedlinesPreservePackageAndExactText(t *testing.T) {
	parts := map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>⚖️ Master Agreement</w:t></w:r></w:p><w:p><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">Provider's liability is </w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t>unlimited for all claims.</w:t></w:r><w:r><w:tab/><w:t>Survival applies.</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Table terms stay intact.</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:sectPr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr></w:body></w:document>`,
		"word/styles.xml":   `<styles><style name="FirmTemplate"/></styles>`, "word/header1.xml": `<header>Firm header</header>`, "word/media/logo.png": "binary-logo-fixture", "word/_rels/document.xml.rels": `<relationships/>`, "[Content_Types].xml": `<types/>`,
	}
	quote := "Provider's liability is unlimited for all claims."
	result, err := redlineDOCX(zipFixture(t, parts), []M{{"ref": "r1", "kind": "replace", "span_start": 20, "original_text": quote, "proposed_text": "Provider's liability is capped at fees paid."}}, "Reviewer & Partner")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Skipped) != 0 {
		t.Fatal(result.Applied, result.Skipped)
	}
	out := zipParts(t, result.Content)
	for name, original := range parts {
		if name != "word/document.xml" && out[name] != original {
			t.Errorf("unrelated package part changed: %s", name)
		}
	}
	before := revisionView(t, parts["word/document.xml"], true)
	if rejected := revisionView(t, out["word/document.xml"], false); rejected != before {
		t.Fatalf("rejecting revisions changed original:\n%q\n%q", before, rejected)
	}
	want := strings.Replace(before, quote, "Provider's liability is capped at fees paid.", 1)
	if accepted := revisionView(t, out["word/document.xml"], true); accepted != want {
		t.Fatalf("accepted text differs:\nwant %q\ngot %q", want, accepted)
	}
	for _, original := range []string{`<w:pPr><w:pStyle w:val="Heading1"/></w:pPr>`, `<w:tbl><w:tr><w:tc>`, `<w:rPr><w:b/></w:rPr>`, `<w:rPr><w:i/></w:rPr>`, `w:author="Reviewer &amp; Partner"`} {
		if !strings.Contains(out["word/document.xml"], original) {
			t.Errorf("format or attribution lost: %s", original)
		}
	}
}

func TestRedlinesAcrossParagraphsAndAmbiguousAnchors(t *testing.T) {
	base := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Alpha first.</w:t></w:r></w:p><w:p><w:r><w:t>Beta second.</w:t></w:r></w:p><w:p><w:r><w:t>Repeat. Repeat.</w:t></w:r></w:p></w:body></w:document>`
	result, err := redlineDOCX(zipFixture(t, map[string]string{"word/document.xml": base}), []M{{"ref": "cross", "original_text": "first.\nBeta", "proposed_text": "replacement"}, {"ref": "ambiguous", "original_text": "Repeat.", "proposed_text": "Changed."}, {"ref": "missing", "original_text": "not in original", "proposed_text": "Invented"}}, "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || len(result.Skipped) != 2 {
		t.Fatal(result.Applied, result.Skipped)
	}
	xml := zipParts(t, result.Content)["word/document.xml"]
	if !strings.HasPrefix(revisionView(t, xml, false), "Alpha first.\nBeta second.\nRepeat. Repeat.\n") {
		t.Fatal("cross-paragraph deletion changed original")
	}
	if !strings.Contains(xml, "unapplied proposals") || !strings.Contains(xml, "ambiguous") {
		t.Fatal("unapplied proposals were hidden")
	}
}
