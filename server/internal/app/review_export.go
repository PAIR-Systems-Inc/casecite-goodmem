package app

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

var reviewCitationHeaders = []string{"Document", "Field", "Value", "Quote", "Span start", "Span end", "Verified", "Status", "Error"}
var reviewReviewHeaders = []string{"Document", "Field", "Model value", "Override", "Effective value", "Review status", "Reviewed by", "Reviewed at", "Note"}
var reviewSummaryHeaders = []string{"Field", "Verified", "Unverified", "Not found", "Errors", "Approved", "Flagged", "Overridden"}
var reviewStatusLabels = map[string]string{"extracted": "verified", "unverified": "unverified", "not_found": "not found", "error": "error"}

func reviewEffective(c M) any {
	if c["override_value"] != nil {
		return c["override_value"]
	}
	return c["value"]
}
func reviewDisplay(c M) string {
	if c == nil {
		return ""
	}
	if v, ok := reviewEffective(c).(string); ok && v != "" {
		return v
	}
	if str(c, "status") == "not_found" {
		return "—"
	}
	if str(c, "status") == "error" {
		return "error"
	}
	return ""
}
func reviewExportSelection(fields, cells []M, keys []string, reviewed bool, statuses []string) ([]M, []M) {
	chosen := []M{}
	chosenKeys := []string{}
	for _, f := range fields {
		if len(keys) == 0 || slices.Contains(keys, str(f, "key")) {
			chosen = append(chosen, f)
			chosenKeys = append(chosenKeys, str(f, "key"))
		}
	}
	out := []M{}
	for _, c := range cells {
		if !slices.Contains(chosenKeys, str(c, "field_key")) || (reviewed && str(c, "review_status") != "approved") {
			continue
		}
		if len(statuses) > 0 && !slices.Contains(statuses, str(c, "status")) && !slices.Contains(statuses, str(c, "review_status")) {
			continue
		}
		out = append(out, c)
	}
	return chosen, out
}
func reviewExportSummary(fields, cells []M) []M {
	out := []M{}
	for _, f := range fields {
		r := M{"field": f["name"], "key": f["key"], "verified": 0, "unverified": 0, "not_found": 0, "errors": 0, "approved": 0, "flagged": 0, "overridden": 0}
		for _, c := range cells {
			if c["field_key"] != f["key"] {
				continue
			}
			status := str(c, "status")
			if status == "extracted" {
				status = "verified"
			}
			if status == "error" {
				status = "errors"
			}
			if _, ok := r[status]; ok {
				r[status] = number(r, status, 0) + 1
			}
			if status = str(c, "review_status"); status == "approved" || status == "flagged" {
				r[status] = number(r, status, 0) + 1
			}
			if c["override_value"] != nil {
				r["overridden"] = number(r, "overridden", 0) + 1
			}
		}
		out = append(out, r)
	}
	return out
}
func reviewCitationRow(doc, field, cell M, quotes bool) []any {
	quote := ""
	if quotes {
		quote = str(cell, "quote")
	}
	verified := "no"
	if v, _ := cell["verified"].(bool); v {
		verified = "yes"
	}
	return []any{doc["filename"], field["name"], str(cell, "value"), quote, cell["span_start"], cell["span_end"], verified, fallback(reviewStatusLabels[str(cell, "status")], str(cell, "status")), str(cell, "error")}
}
func reviewReviewRow(doc, field, cell M) []any {
	return []any{doc["filename"], field["name"], str(cell, "value"), str(cell, "override_value"), reviewEffective(cell), fallback(str(cell, "review_status"), "pending"), str(cell, "reviewed_by"), str(cell, "reviewed_at"), str(cell, "note")}
}
func reviewCellIndex(cells []M) map[[2]string]M {
	out := map[[2]string]M{}
	for _, c := range cells {
		out[[2]string{str(c, "document_id"), str(c, "field_key")}] = c
	}
	return out
}

// CSV does not encode a text cell type. Protect user/model/source strings from
// spreadsheet formula interpretation, including a leading whitespace bypass.
func reviewCSVValue(value any) string {
	if value == nil {
		return ""
	}
	s := fmt.Sprint(value)
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}
func reviewCSV(fields, docs, cells []M, quotes bool) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	headers := append(slices.Clone(reviewCitationHeaders), "Override", "Review status", "Reviewed by", "Note")
	if err := w.Write(headers); err != nil {
		return nil, err
	}
	index := reviewCellIndex(cells)
	for _, doc := range docs {
		for _, f := range fields {
			c := index[[2]string{str(doc, "id"), str(f, "key")}]
			row := []any{doc["filename"], f["name"], "", "", "", "", "", "", "", "", "", "", ""}
			if c != nil {
				row = append(reviewCitationRow(doc, f, c, quotes), str(c, "override_value"), fallback(str(c, "review_status"), "pending"), str(c, "reviewed_by"), str(c, "note"))
			}
			record := []string{}
			for _, v := range row {
				record = append(record, reviewCSVValue(v))
			}
			if err := w.Write(record); err != nil {
				return nil, err
			}
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
func reviewJSON(table M, fields, docs, cells []M, quotes bool) ([]byte, error) {
	out := []M{}
	for _, c := range cells {
		item := M{}
		for _, key := range []string{"document_id", "field_key", "value", "override_value", "status", "verified", "span_start", "span_end", "review_status", "reviewed_by", "reviewed_at", "note", "error", "model_used", "updated_at"} {
			item[key] = c[key]
		}
		item["effective_value"] = reviewEffective(c)
		if quotes {
			item["quote"] = c["quote"]
		}
		out = append(out, item)
	}
	return json.MarshalIndent(M{"table": M{"id": table["id"], "name": table["name"], "description": table["description"]}, "fields": fields, "documents": docs, "cells": out, "summary": reviewExportSummary(fields, cells)}, "", "  ")
}

func reviewXLSX(name string, fields, docs, cells []M, quotes bool) ([]byte, error) {
	w := excelize.NewFile()
	defer w.Close()
	var firstErr error
	check := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	check(w.SetSheetName("Sheet1", "Results"))
	for _, sheet := range []string{"Citations", "Review", "Summary"} {
		_, err := w.NewSheet(sheet)
		check(err)
	}
	style := func(v *excelize.Style) int { id, err := w.NewStyle(v); check(err); return id }
	header := style(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E8ECF2"}}})
	wrap := style(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	cellStyle := map[string]int{}
	for _, override := range []bool{false, true} {
		for _, status := range []string{"extracted", "unverified", "error", "not_found"} {
			font := &excelize.Font{Color: "1F4E99", Underline: "single"}
			if override {
				font = &excelize.Font{Color: "0F766E", Bold: true}
			}
			s := &excelize.Style{Font: font}
			if status == "unverified" {
				s.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFF4D6"}}
			}
			if status == "error" {
				s.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FBE3E3"}}
			}
			cellStyle[fmt.Sprint(override)+status] = style(s)
		}
	}
	set := func(sheet string, row, col int, value any) string {
		axis, err := excelize.CoordinatesToCellName(col, row)
		check(err)
		// Explicit string writes fix upstream's formula injection in exported
		// model values, field names, filenames, notes and source quotations.
		if text, ok := value.(string); ok {
			check(w.SetCellStr(sheet, axis, text))
		} else {
			check(w.SetCellValue(sheet, axis, value))
		}
		return axis
	}
	headers := map[string][]string{"Results": {"Document"}, "Citations": reviewCitationHeaders, "Review": reviewReviewHeaders, "Summary": reviewSummaryHeaders}
	for _, f := range fields {
		headers["Results"] = append(headers["Results"], str(f, "name"))
	}
	for _, sheet := range []string{"Results", "Citations", "Review", "Summary"} {
		for i, h := range headers[sheet] {
			axis := set(sheet, 1, i+1, h)
			check(w.SetCellStyle(sheet, axis, axis, header))
		}
		panes := &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}
		if sheet == "Results" {
			panes.XSplit = 1
			panes.TopLeftCell = "B2"
			panes.ActivePane = "bottomRight"
		}
		check(w.SetPanes(sheet, panes))
	}
	index := reviewCellIndex(cells)
	citationRow, reviewRow := 2, 2
	for i, doc := range docs {
		set("Results", i+2, 1, doc["filename"])
		for j, f := range fields {
			c := index[[2]string{str(doc, "id"), str(f, "key")}]
			axis := set("Results", i+2, j+2, reviewDisplay(c))
			if c == nil {
				continue
			}
			for k, v := range reviewReviewRow(doc, f, c) {
				set("Review", reviewRow, k+1, v)
			}
			reviewRow++
			if str(c, "status") == "not_found" && c["override_value"] == nil {
				continue
			}
			for k, v := range reviewCitationRow(doc, f, c, quotes) {
				set("Citations", citationRow, k+1, v)
			}
			check(w.SetCellHyperLink("Results", axis, fmt.Sprintf("'Citations'!A%d", citationRow), "Location"))
			check(w.SetCellStyle("Results", axis, axis, cellStyle[fmt.Sprint(c["override_value"] != nil)+str(c, "status")]))
			quoteAxis := fmt.Sprintf("D%d", citationRow)
			check(w.SetCellStyle("Citations", quoteAxis, quoteAxis, wrap))
			citationRow++
		}
	}
	for i, row := range reviewExportSummary(fields, cells) {
		for j, k := range []string{"field", "verified", "unverified", "not_found", "errors", "approved", "flagged", "overridden"} {
			set("Summary", i+2, j+1, row[k])
		}
	}
	widths := map[string][]float64{"Results": {36}, "Citations": {36, 24, 28, 60, 10, 10, 9, 11, 30}, "Review": {36, 24, 28, 28, 28, 14, 24, 20, 40}, "Summary": {28, 10, 11, 10, 8, 10, 9, 11}}
	for range fields {
		widths["Results"] = append(widths["Results"], 28)
	}
	for _, sheet := range []string{"Results", "Citations", "Review", "Summary"} {
		for i, width := range widths[sheet] {
			col, err := excelize.ColumnNumberToName(i + 1)
			check(err)
			check(w.SetColWidth(sheet, col, col, width))
		}
	}
	check(w.SetDocProps(&excelize.DocProperties{Title: name, Creator: "CaseCite"}))
	if firstErr != nil {
		return nil, firstErr
	}
	buf, err := w.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func reviewCSVList(raw string) []string {
	out := []string{}
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

var reviewExportStemRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) reviewTableExport(q request) (any, error) {
	query := q.r.URL.Query()
	format := fallback(query.Get("format"), "xlsx")
	if !slices.Contains([]string{"xlsx", "csv", "json"}, format) {
		return nil, fail(422, "Export format must be xlsx, csv or json")
	}
	quotes, reviewed := true, false
	var err error
	if query.Has("include_quotes") {
		quotes, err = strconv.ParseBool(query.Get("include_quotes"))
		if err != nil {
			return nil, fail(422, "Invalid include_quotes")
		}
	}
	if query.Has("reviewed_only") {
		reviewed, err = strconv.ParseBool(query.Get("reviewed_only"))
		if err != nil {
			return nil, fail(422, "Invalid reviewed_only")
		}
	}
	statuses := reviewCSVList(query.Get("status"))
	for _, status := range statuses {
		if reviewStatusLabels[status] == "" && status != "approved" && status != "flagged" {
			return nil, bad("Unknown status filter")
		}
	}
	table, err := s.getObject(q.r.Context(), q.user.ID, "review_table", q.r.PathValue("table_id"))
	if err != nil {
		return nil, err
	}
	cells, err := s.reviewCells(q.r.Context(), str(table, "id"))
	if err != nil {
		return nil, err
	}
	fields, cells := reviewExportSelection(maps(table["fields"]), cells, reviewCSVList(query.Get("fields")), reviewed, statuses)
	if len(fields) == 0 {
		return nil, bad("None of the given fields is in this table.")
	}
	docs := s.reviewDocuments(q.r.Context(), q.user.ID, table)
	var content []byte
	media := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	switch format {
	case "csv":
		content, err = reviewCSV(fields, docs, cells, quotes)
		media = "text/csv; charset=utf-8"
	case "json":
		content, err = reviewJSON(table, fields, docs, cells, quotes)
		media = "application/json"
	default:
		content, err = reviewXLSX(str(table, "name"), fields, docs, cells, quotes)
	}
	if err != nil {
		return nil, err
	}
	stem := fallback(charSlice(strings.Trim(reviewExportStemRE.ReplaceAllString(str(table, "name"), "-"), "-"), 0, 60), "review")
	s.audit(q.r.Context(), q.user.ID, "review_table.export", "review_table", str(table, "id"), M{"format": format, "cells": len(cells), "fields": len(fields), "reviewed_only": reviewed, "include_quotes": quotes})
	attachment(q.w, stem+"."+format, media, content)
	return nil, nil
}
