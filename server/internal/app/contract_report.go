package app

import (
	"archive/zip"
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

const aiReviewNotice = "AI-generated draft. Verify every statement, citation and clause against the source documents and current law before relying on it or sending it to anyone."

type reportBlock struct {
	Kind, Text string
	Rows       [][]string
}

func fieldLabel(field string) string {
	words := strings.Fields(strings.ReplaceAll(field, "_", " "))
	for i, word := range words {
		r := []rune(word)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}
func contractReportBlocks(data M) []reportBlock {
	blocks := []reportBlock{{Kind: "title", Text: "Contract Analysis Report"}}
	for _, key := range []string{"contract_type", "created_at", "representing", "posture"} {
		value := str(data, key)
		if value == "" {
			continue
		}
		label := fieldLabel(key)
		if key == "created_at" {
			label = "Analysis date"
			value = charSlice(value, 0, 10)
		}
		blocks = append(blocks, reportBlock{Kind: "meta", Text: label + ": " + value})
	}
	if summary := strings.TrimSpace(str(data, "executive_summary")); summary != "" {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Executive Summary"}, reportBlock{Kind: "text", Text: summary})
	}
	terms := object(data["key_terms"])
	if len(terms) > 0 {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Key Terms"})
		rows := [][]string{{"Term", "Value"}}
		keys := slices.Clone(contractKeyFields)
		for key := range terms {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
		for _, key := range keys {
			term, ok := terms[key].(map[string]any)
			if !ok {
				continue
			}
			value := fallback(strings.TrimSpace(str(term, "value")), "—")
			if term["verified"] != true {
				value += " (unverified)"
			} else if quote := strings.TrimSpace(str(term, "quote")); quote != "" {
				value += "\n\"" + quote + "\""
			}
			rows = append(rows, []string{fieldLabel(key), value})
		}
		blocks = append(blocks, reportBlock{Kind: "table", Rows: rows})
	}
	issues := maps(data["issues"])
	if len(issues) > 0 {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Issues"})
		order := []string{"critical", "major", "minor"}
		groups := map[string][]M{}
		for _, issue := range issues {
			sev := strings.ToLower(fallback(str(issue, "severity"), "minor"))
			if !slices.Contains(order, sev) {
				order = append(order, sev)
			}
			groups[sev] = append(groups[sev], issue)
		}
		for _, sev := range order {
			if len(groups[sev]) == 0 {
				continue
			}
			blocks = append(blocks, reportBlock{Kind: "subheading", Text: fieldLabel(sev)})
			for _, issue := range groups[sev] {
				title := fallback(strings.TrimSpace(str(issue, "title")), "Issue")
				if str(issue, "grounding") == "unverified" {
					title += " (unverified — model conclusion)"
				}
				blocks = append(blocks, reportBlock{Kind: "bulletbold", Text: title})
				for _, key := range []string{"why", "suggested_language"} {
					if value := strings.TrimSpace(str(issue, key)); value != "" {
						blocks = append(blocks, reportBlock{Kind: "nested", Text: fieldLabel(key) + ": " + value})
					}
				}
				if str(issue, "grounding") == "absence" {
					blocks = append(blocks, reportBlock{Kind: "quote", Text: "Clause not found in document."})
				} else if quote := strings.TrimSpace(str(issue, "matched_text")); quote != "" && str(issue, "grounding") == "span" {
					blocks = append(blocks, reportBlock{Kind: "quote", Text: "\"" + quote + "\""})
				}
			}
		}
	}
	if parties := maps(data["parties"]); len(parties) > 0 {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Parties"})
		for _, party := range parties {
			name := fallback(str(party, "canonical_name"), "Unknown party")
			if role := str(party, "role"); role != "" {
				name += " (" + role + ")"
			}
			blocks = append(blocks, reportBlock{Kind: "bullet", Text: name})
		}
	}
	if obligations := maps(data["obligations"]); len(obligations) > 0 {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Obligations"})
		for _, obligation := range obligations {
			parts := []string{fallback(strings.TrimSpace(str(obligation, "subject_party")), "Unspecified party")}
			for _, key := range []string{"action", "object_text"} {
				if value := strings.TrimSpace(str(obligation, key)); value != "" {
					parts = append(parts, value)
				}
			}
			blocks = append(blocks, reportBlock{Kind: "bullet", Text: strings.Join(parts, " — ")})
			deadlines := []string{}
			switch entries := obligation["deadlines"].(type) {
			case []string:
				deadlines = entries
			case []M:
				for _, entry := range entries {
					deadlines = append(deadlines, linkedDeadlineText(entry))
				}
			case []any:
				for _, entry := range entries {
					deadlines = append(deadlines, linkedDeadlineText(entry))
				}
			}
			for _, deadline := range deadlines {
				blocks = append(blocks, reportBlock{Kind: "nested", Text: "Deadline: " + deadline})
			}
		}
	}
	if deadlines := maps(data["deadlines"]); len(deadlines) > 0 {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Deadlines"})
		for _, deadline := range deadlines {
			description := fallback(str(deadline, "description"), fallback(str(deadline, "matched_text"), "Deadline"))
			if date := str(deadline, "resolved_date"); date != "" {
				description += " — resolved date: " + charSlice(date, 0, 10)
			}
			blocks = append(blocks, reportBlock{Kind: "bullet", Text: description})
		}
	}
	if terms := maps(data["defined_terms"]); len(terms) > 0 {
		blocks = append(blocks, reportBlock{Kind: "heading", Text: "Defined Terms"})
		for _, term := range terms {
			flags := []string{}
			for _, key := range []string{"used_but_undefined", "defined_but_unused", "circular_reference"} {
				if term[key] == true {
					flags = append(flags, strings.ReplaceAll(key, "_", " "))
				}
			}
			text := str(term, "term")
			if definition := str(term, "definition_text"); definition != "" {
				text += " — " + definition
			}
			if len(flags) > 0 {
				text += " (" + strings.Join(flags, ", ") + ")"
			}
			blocks = append(blocks, reportBlock{Kind: "bullet", Text: text})
		}
	}
	for _, warning := range stringsOf(data["warnings"]) {
		blocks = append(blocks, reportBlock{Kind: "text", Text: "Review limitation: " + warning})
	}
	blocks = append(blocks, reportBlock{Kind: "notice", Text: aiReviewNotice})
	return blocks
}
func linkedDeadlineText(entry any) string {
	if m, ok := entry.(map[string]any); ok {
		for _, key := range []string{"description", "matched_text", "kind"} {
			if v := str(m, key); v != "" {
				return v
			}
		}
		keys := []string{}
		for key := range m {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		parts := []string{}
		for _, key := range keys {
			parts = append(parts, key+": "+fmt.Sprint(m[key]))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(entry)
}
func reportMarkdown(blocks []reportBlock) string {
	var out strings.Builder
	for _, block := range blocks {
		switch block.Kind {
		case "title":
			out.WriteString("# " + block.Text + "\n\n")
		case "heading":
			out.WriteString("## " + block.Text + "\n\n")
		case "subheading":
			out.WriteString("### " + block.Text + "\n\n")
		case "meta":
			label, value, _ := strings.Cut(block.Text, ": ")
			out.WriteString("**" + label + ":** " + value + "  \n\n")
		case "bullet":
			out.WriteString("- " + block.Text + "\n")
		case "bulletbold":
			out.WriteString("\n- **" + block.Text + "**\n")
		case "nested":
			out.WriteString("  - " + block.Text + "\n")
		case "quote":
			out.WriteString("\n> " + strings.ReplaceAll(block.Text, "\n", "\n> ") + "\n\n")
		case "notice":
			out.WriteString("\n---\n\n_" + block.Text + "_\n")
		case "table":
			for i, row := range block.Rows {
				cells := []string{}
				for _, cell := range row {
					cells = append(cells, strings.NewReplacer("|", "\\|", "\n", "<br>").Replace(cell))
				}
				out.WriteString("| " + strings.Join(cells, " | ") + " |\n")
				if i == 0 {
					out.WriteString("|" + strings.Repeat(" --- |", len(row)) + "\n")
				}
			}
			out.WriteByte('\n')
		default:
			out.WriteString(block.Text + "\n\n")
		}
	}
	return out.String()
}
func wordParagraph(text, style string, bold, italic bool) string {
	var out strings.Builder
	out.WriteString("<w:p>")
	if style != "" {
		out.WriteString(`<w:pPr><w:pStyle w:val="` + xmlText(style) + `"/></w:pPr>`)
	}
	out.WriteString("<w:r>")
	if bold || italic {
		out.WriteString("<w:rPr>")
		if bold {
			out.WriteString("<w:b/>")
		}
		if italic {
			out.WriteString("<w:i/>")
		}
		out.WriteString("</w:rPr>")
	}
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if i > 0 {
			out.WriteString("<w:br/>")
		}
		out.WriteString(`<w:t xml:space="preserve">` + xmlText(line) + `</w:t>`)
	}
	out.WriteString("</w:r></w:p>")
	return out.String()
}
func reportWordDocument(blocks []reportBlock) ([]byte, error) {
	var body strings.Builder
	numbering := `<w:numbering xmlns:w="` + wordNamespace + `">`
	for id, format := range []string{"bullet", "decimal"} {
		label := "•"
		if format == "decimal" {
			label = "%1."
		}
		numbering += fmt.Sprintf(`<w:abstractNum w:abstractNumId="%d"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="%s"/><w:suff w:val="space"/><w:lvlText w:val="%s "/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="360"/></w:tabs><w:ind w:left="360" w:hanging="180"/></w:pPr></w:lvl></w:abstractNum>`, id, format, label)
	}
	listID, previous := 0, ""
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="` + wordNamespace + `"><w:body>`)
	for _, block := range blocks {
		switch block.Kind {
		case "title":
			body.WriteString(wordParagraph(block.Text, "Title", false, false))
		case "heading":
			body.WriteString(wordParagraph(block.Text, "Heading1", false, false))
		case "subheading":
			body.WriteString(wordParagraph(block.Text, "Heading2", false, false))
		case "meta":
			body.WriteString(wordParagraph(block.Text, "Normal", true, false))
		case "stamp":
			body.WriteString(wordParagraph(block.Text, "Normal", false, true))
		case "ordered", "unordered":
			if previous != block.Kind {
				listID++
				abstract := 0
				if block.Kind == "ordered" {
					abstract = 1
				}
				numbering += fmt.Sprintf(`<w:num w:numId="%d"><w:abstractNumId w:val="%d"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`, listID, abstract)
			}
			paragraph := wordParagraph(block.Text, "ListParagraph", false, false)
			body.WriteString(strings.Replace(paragraph, `</w:pPr>`, fmt.Sprintf(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="%d"/></w:numPr></w:pPr>`, listID), 1))
		case "bullet", "bulletbold":
			body.WriteString(wordParagraph("• "+block.Text, "ListParagraph", block.Kind == "bulletbold", false))
		case "nested":
			body.WriteString(wordParagraph("– "+block.Text, "NestedList", false, false))
		case "quote":
			body.WriteString(wordParagraph(block.Text, "Quote", false, true))
		case "notice":
			body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="999999"/></w:pBdr></w:pPr></w:p>`)
			body.WriteString(wordParagraph(block.Text, "Normal", false, true))
		case "table":
			body.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/><w:tblBorders>`)
			for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
				body.WriteString(`<w:` + side + ` w:val="single" w:sz="4" w:color="B7C4CF"/>`)
			}
			body.WriteString(`</w:tblBorders></w:tblPr><w:tblGrid><w:gridCol w:w="2400"/><w:gridCol w:w="6960"/></w:tblGrid>`)
			for i, row := range block.Rows {
				body.WriteString("<w:tr>")
				if i == 0 {
					body.WriteString("<w:trPr><w:tblHeader/></w:trPr>")
				}
				for j, cell := range row {
					width := 6960
					if j == 0 {
						width = 2400
					}
					body.WriteString(fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/>`, width))
					if i == 0 {
						body.WriteString(`<w:shd w:fill="E9EFF5"/>`)
					}
					body.WriteString("</w:tcPr>" + wordParagraph(cell, "Normal", i == 0, false) + "</w:tc>")
				}
				body.WriteString("</w:tr>")
			}
			body.WriteString("</w:tbl>")
		default:
			body.WriteString(wordParagraph(block.Text, "Normal", false, false))
		}
		previous = block.Kind
	}
	body.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720"/></w:sectPr></w:body></w:document>`)
	styles := `<?xml version="1.0"?><w:styles xmlns:w="` + wordNamespace + `"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>`
	for _, style := range []struct {
		id, size string
		level    int
	}{{"Title", "40", 0}, {"Heading1", "32", 0}, {"Heading2", "26", 1}} {
		styles += `<w:style w:type="paragraph" w:styleId="` + style.id + `"><w:name w:val="` + style.id + `"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="120"/>` + fmt.Sprintf(`<w:outlineLvl w:val="%d"/>`, style.level) + `</w:pPr><w:rPr><w:b/><w:color w:val="000000"/><w:sz w:val="` + style.size + `"/></w:rPr></w:style>`
	}
	for _, style := range []struct{ id, indent string }{{"ListParagraph", "360"}, {"NestedList", "720"}, {"Quote", "720"}} {
		styles += `<w:style w:type="paragraph" w:styleId="` + style.id + `"><w:name w:val="` + style.id + `"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="` + style.indent + `"/></w:pPr></w:style>`
	}
	styles += "</w:styles>"
	files := map[string]string{"word/document.xml": body.String(), "word/styles.xml": styles, "[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`, "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`, "word/_rels/document.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`}
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	parts := []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/_rels/document.xml.rels"}
	if listID > 0 {
		files["word/numbering.xml"] = numbering + `</w:numbering>`
		files["[Content_Types].xml"] = strings.Replace(files["[Content_Types].xml"], `</Types>`, `<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/></Types>`, 1)
		files["word/_rels/document.xml.rels"] = strings.Replace(files["word/_rels/document.xml.rels"], `</Relationships>`, `<Relationship Id="rIdNumbering" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/></Relationships>`, 1)
		parts = append(parts, "word/numbering.xml")
	}
	for _, name := range parts {
		entry, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = entry.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
