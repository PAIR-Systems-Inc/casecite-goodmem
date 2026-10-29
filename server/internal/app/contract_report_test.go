package app

import (
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractReportsRetainFindingsAndGrounding(t *testing.T) {
	data := M{
		"contract_type": "services", "created_at": "2026-10-29T12:00:00Z", "representing": "Northstar LLC", "posture": "balanced",
		"executive_summary": "The payment and liability provisions require review. The governing-law clause is present.",
		"key_terms":         M{"payment": M{"value": "Net 30", "quote": "Invoices are payable within 30 days.", "verified": true}, "term": M{"value": "Two years", "verified": false}},
		"issues": []M{
			{"severity": "major", "title": "Unlimited liability", "why": "The cap has no upper limit.", "suggested_language": "Limit liability to fees paid in the preceding 12 months.", "grounding": "span", "matched_text": "Provider liability is unlimited."},
			{"severity": "minor", "title": "Missing assignment terms", "grounding": "absence", "suggested_language": "Neither party may assign without written consent."},
			{"severity": "info", "title": "Model observation", "grounding": "unverified", "matched_text": "A fabricated quotation must not appear."},
		},
		"parties":       []M{{"canonical_name": "Northstar LLC", "role": "customer"}, {"canonical_name": "Acme, Inc.", "role": "provider"}},
		"obligations":   []M{{"subject_party": "Customer", "action": "pay invoices", "object_text": "within 30 days", "deadlines": []any{M{"description": "30 days after receipt"}}}},
		"deadlines":     []M{{"description": "Pay the first invoice", "resolved_date": "2026-11-28T00:00:00Z"}},
		"defined_terms": []M{{"term": "Service Period", "defined_but_unused": true, "definition_text": "The initial twelve-month period."}},
		"warnings":      []string{"Provider fixture; no live legal assessment."},
	}
	blocks := contractReportBlocks(data)
	markdown := reportMarkdown(blocks)
	docx, err := reportWordDocument(blocks)
	if err != nil {
		t.Fatal(err)
	}
	parts := zipParts(t, docx)
	for name, raw := range parts {
		d := xml.NewDecoder(strings.NewReader(raw))
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid %s: %v", name, err)
			}
		}
	}
	visible := revisionView(t, parts["word/document.xml"], true)
	for _, want := range []string{"Contract Analysis Report", "Executive Summary", "Key Terms", "Two years (unverified)", "Provider liability is unlimited.", "Clause not found in document.", "Model observation (unverified — model conclusion)", "Northstar LLC", "30 days after receipt", "2026-11-28", "Service Period", "defined but unused", aiReviewNotice} {
		for _, output := range []string{markdown, visible} {
			if !strings.Contains(output, want) {
				t.Errorf("report omitted %q", want)
			}
		}
	}
	if strings.Contains(markdown, "A fabricated quotation") || strings.Contains(visible, "A fabricated quotation") {
		t.Fatal("unverified quote presented as source evidence")
	}
	if !strings.Contains(parts["word/document.xml"], "<w:tblHeader/>") || !strings.Contains(parts["word/styles.xml"], `w:styleId="Heading1"`) {
		t.Fatal("report lost table headers or heading styles")
	}
	// Optional local artifacts for rendering with an independent office engine.
	// CI uses the structural/content assertions above and does not need LibreOffice.
	if dir := os.Getenv("PARITY_ARTIFACT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "contract-report.docx"), docx, 0600); err != nil {
			t.Fatal(err)
		}
		redlined, err := redlineDOCX(docx, []M{{"ref": "terms", "original_text": "Invoices are payable within 30 days.", "proposed_text": "Invoices are payable within 45 days."}}, "CaseCite reviewer", true)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "contract-redlines.docx"), redlined.Content, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
