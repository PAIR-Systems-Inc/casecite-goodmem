package app

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestReviewTableUpstreamOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/review_tables.json")
	if err != nil {
		t.Fatal(err)
	}
	data := M{}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	equal := func(t *testing.T, want, got any) {
		t.Helper()
		if string(jsonBytes(want)) != string(jsonBytes(got)) {
			t.Fatalf("want %s\ngot %s", jsonBytes(want), jsonBytes(got))
		}
	}
	for i, c := range maps(data["cells"]) {
		t.Run(fmt.Sprintf("cell-%d", i), func(t *testing.T) { equal(t, c["expected"], reviewModelCell(str(c, "text"), c["raw"])) })
	}
	for i, c := range maps(data["excerpts"]) {
		t.Run(fmt.Sprintf("excerpts-%d", i), func(t *testing.T) {
			out := [][2]string{}
			for _, e := range reviewExcerpts(str(c, "text"), maps(c["fields"]), maps(c["tags"])) {
				out = append(out, [2]string{e.Label, e.Text})
			}
			equal(t, c["expected"], out)
		})
	}
	for i, c := range maps(data["exports"]) {
		t.Run(fmt.Sprintf("export-%d", i), func(t *testing.T) {
			reviewed, _ := c["reviewed_only"].(bool)
			fields, cells := reviewExportSelection(maps(c["fields"]), maps(c["cells"]), stringsOf(c["field_keys"]), reviewed, stringsOf(c["statuses"]))
			equal(t, c["expected_fields"], fields)
			equal(t, c["expected_cells"], cells)
			equal(t, c["summary"], reviewExportSummary(fields, cells))
		})
	}
	for _, c := range maps(data["estimates"]) {
		if got := reviewEstimateTokens(number(c, "chars", 0), number(c, "fields", 0)); got != number(c, "expected", 0) {
			t.Fatalf("estimate %v = %d", c, got)
		}
	}
}

func TestReviewExportsCitationsOverridesAndFormulaSafety(t *testing.T) {
	fields := []M{{"key": "law", "name": "Governing law"}, {"key": "price", "name": "Price"}, {"key": "missing", "name": "Missing"}}
	docs := []M{{"id": "d1", "filename": "agreement.txt"}}
	cells := []M{
		{"document_id": "d1", "field_key": "law", "value": "Delaware", "quote": "The laws of Delaware apply.", "span_start": 12, "span_end": 38, "verified": true, "status": "extracted", "override_value": "Texas", "review_status": "approved", "note": "Check conflict of laws"},
		{"document_id": "d1", "field_key": "price", "value": "=HYPERLINK(\"https://attacker.invalid\",\"Click\")", "quote": "\t=malicious()", "status": "unverified", "review_status": "flagged"},
		{"document_id": "d1", "field_key": "missing", "value": nil, "status": "not_found"},
	}
	raw, err := reviewXLSX("Diligence", fields, docs, cells, true)
	if err != nil {
		t.Fatal(err)
	}
	w, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if strings.Join(w.GetSheetList(), ",") != "Results,Citations,Review,Summary" {
		t.Fatal(w.GetSheetList())
	}
	for _, test := range []struct{ sheet, cell, want string }{{"Results", "B2", "Texas"}, {"Citations", "C2", "Delaware"}, {"Citations", "D2", "The laws of Delaware apply."}, {"Review", "D2", "Texas"}, {"Summary", "F2", "1"}, {"Summary", "G3", "1"}, {"Results", "D2", "—"}} {
		value, err := w.GetCellValue(test.sheet, test.cell)
		if err != nil || value != test.want {
			t.Fatalf("%s!%s = %q, %v", test.sheet, test.cell, value, err)
		}
	}
	link, target, err := w.GetCellHyperLink("Results", "B2")
	if err != nil || !link || target != "'Citations'!A2" {
		t.Fatal(link, target, err)
	}
	panes, err := w.GetPanes("Results")
	if err != nil || !panes.Freeze || panes.TopLeftCell != "B2" {
		t.Fatal(panes, err)
	}
	// Independent OOXML walk: no data was serialized as an executable formula.
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range z.File {
		if !strings.HasPrefix(part.Name, "xl/worksheets/") || !strings.HasSuffix(part.Name, ".xml") {
			continue
		}
		r, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(r)
		for {
			tok, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "f" {
				t.Fatal("formula in", part.Name)
			}
		}
		r.Close()
	}
	flat, err := reviewCSV(fields, docs, cells, false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(flat)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1][3] != "" || !strings.HasPrefix(rows[2][2], "'=HYPERLINK") {
		t.Fatal(rows)
	}
	jsonData, err := reviewJSON(M{"id": "t", "name": "Diligence"}, fields, docs, cells, false)
	if err != nil {
		t.Fatal(err)
	}
	out := M{}
	json.Unmarshal(jsonData, &out)
	for _, c := range maps(out["cells"]) {
		if _, exists := c["quote"]; exists {
			t.Fatal("quotes were not excluded")
		}
	}
}

func TestReviewHumanDecisionsSurviveReruns(t *testing.T) {
	c := newReviewCell("doc", "law")
	applyReviewModel(c, reviewModelCell("Delaware governs.", M{"value": "Delaware", "quote": "Delaware governs."}), "model", 17, "runner")
	applyReviewChange(c, M{"review_status": "approved", "note": "Confirmed"}, "reviewer")
	if applyReviewModel(c, reviewModelCell("Delaware governs.", M{"value": "Delaware", "quote": "Delaware governs."}), "model", 17, "runner") {
		t.Fatal("same answer marked changed")
	}
	if str(c, "review_status") != "approved" {
		t.Fatal(c)
	}
	applyReviewModel(c, reviewModelCell("Texas governs.", M{"value": "Texas", "quote": "Texas governs."}), "model", 14, "runner")
	if str(c, "review_status") != "pending" || str(c, "note") != "Confirmed" {
		t.Fatal(c)
	}
	applyReviewChange(c, M{"override_value": "Virginia", "review_status": "approved"}, "reviewer")
	applyReviewModel(c, reviewErrorCell("Provider unavailable"), "model", 0, "runner")
	if str(c, "review_status") != "approved" || reviewEffective(c) != "Virginia" {
		t.Fatal(c)
	}
	for i := 0; i < 30; i++ {
		applyReviewChange(c, M{"override_value": fmt.Sprint(i)}, "reviewer")
	}
	if len(maps(c["history"])) != 20 {
		t.Fatal(c)
	}
	applyReviewChange(c, M{"override_value": nil, "note": nil}, "reviewer")
	if c["override_value"] != nil || c["note"] != nil {
		t.Fatal(c)
	}
}

func TestReviewNumericZeroAndBooleanFalseAreValues(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{{float64(0), "0"}, {false, "no"}, {true, "yes"}, {float64(250000), "250000"}} {
		c := reviewModelCell("The fee is 0 and no renewal is permitted.", M{"value": test.value, "quote": "The fee is 0 and no renewal is permitted."})
		if str(c, "value") != test.want || str(c, "status") != "extracted" {
			t.Fatal(c)
		}
	}
}
