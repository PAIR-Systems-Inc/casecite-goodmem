package app

import (
	"io"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// GoodMem's dots.ocr prompt emits table cells as HTML. Normalize a single
// well-formed table before assembling text, computing UTF-16 offsets or
// indexing it. Unknown/malformed structures keep their original text, never a
// partial conversion. HTML is data here and is never executed by the UI.
func normalizeOCRTable(raw string) string {
	if !strings.Contains(strings.ToLower(raw), "<table") || len(raw) > maxExtractedText {
		return raw
	}
	z := html.NewTokenizer(strings.NewReader(raw))
	z.SetMaxBuf(maxExtractedText)
	type cell struct {
		text          string
		width, height int
	}
	grid := map[[2]int]string{}
	occupied := map[[2]int]bool{}
	rowCells := []cell{}
	rowIndex, columns := -1, 0
	tableOpen, tableSeen, rowOpen, cellOpen := false, false, false, false
	caption, skip := false, ""
	depth, tokens := 0, 0
	var value, before, after strings.Builder
	width, height := 1, 1
	flushCell := func() {
		if cellOpen {
			rowCells = append(rowCells, cell{strings.TrimSpace(value.String()), width, height})
			value.Reset()
			cellOpen = false
		}
	}
	flushRow := func() bool {
		flushCell()
		if !rowOpen {
			return true
		}
		x := 0
		for _, c := range rowCells {
			for occupied[[2]int{rowIndex, x}] {
				x++
			}
			if x+c.width > 512 || rowIndex+c.height > 1000 || len(occupied)+c.width*c.height > 100000 {
				return false
			}
			for y := rowIndex; y < rowIndex+c.height; y++ {
				for col := x; col < x+c.width; col++ {
					key := [2]int{y, col}
					if occupied[key] {
						return false
					}
					occupied[key] = true
				}
			}
			grid[[2]int{rowIndex, x}] = c.text
			columns = max(columns, x+c.width)
			x += c.width
		}
		rowCells = nil
		rowOpen = false
		return true
	}
	span := func(t html.Token, key string) (int, bool) {
		for _, a := range t.Attr {
			if a.Key == key {
				v, e := strconv.Atoi(a.Val)
				return v, e == nil && v >= 1 && v <= 512
			}
		}
		return 1, true
	}
	for {
		kind := z.Next()
		tokens++
		if tokens > 100000 {
			return raw
		}
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return raw
			}
			break
		}
		t := z.Token()
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			if kind == html.StartTagToken {
				depth++
				if depth > 256 {
					return raw
				}
			}
			if skip != "" {
				continue
			}
			switch t.Data {
			case "script", "style", "head":
				skip = t.Data
			case "table":
				if tableOpen || tableSeen {
					return raw
				}
				tableOpen, tableSeen = true, true
			case "caption":
				if tableOpen {
					if before.Len() > 0 {
						before.WriteByte('\n')
					}
					caption = true
				}
			case "tr":
				if !tableOpen || !flushRow() {
					return raw
				}
				rowIndex++
				if rowIndex >= 1000 {
					return raw
				}
				rowOpen = true
			case "td", "th":
				if !tableOpen || !rowOpen {
					return raw
				}
				flushCell()
				var ok bool
				width, ok = span(t, "colspan")
				if !ok {
					return raw
				}
				height, ok = span(t, "rowspan")
				if !ok {
					return raw
				}
				cellOpen = true
			case "br", "p", "div", "li":
				if cellOpen && value.Len() > 0 {
					value.WriteByte('\n')
				}
			case "thead", "tbody", "tfoot", "colgroup", "col", "span", "b", "strong", "em", "i", "u", "s", "a", "small", "sup", "sub", "code":
				// Structural and inline formatting do not change cell text.
			default:
				return raw
			}
			// HTML void elements do not have a corresponding end tag.
			if kind == html.StartTagToken && (t.Data == "br" || t.Data == "hr" || t.Data == "img" || t.Data == "meta" || t.Data == "link" || t.Data == "input" || t.Data == "wbr" || t.Data == "col") {
				depth--
			}
		case html.EndTagToken:
			depth = max(0, depth-1)
			if skip != "" {
				if t.Data == skip {
					skip = ""
				}
				continue
			}
			switch t.Data {
			case "td", "th":
				flushCell()
			case "tr":
				if !flushRow() {
					return raw
				}
			case "caption":
				caption = false
			case "table":
				if !tableOpen || !flushRow() {
					return raw
				}
				tableOpen = false
			case "p", "div", "li":
				if cellOpen && value.Len() > 0 {
					value.WriteByte('\n')
				}
			}
		case html.TextToken:
			if skip != "" {
				continue
			}
			if cellOpen {
				value.WriteString(t.Data)
			} else if caption || !tableSeen {
				before.WriteString(t.Data)
			} else if !tableOpen {
				after.WriteString(t.Data)
			} else if strings.TrimSpace(t.Data) != "" {
				// Unexpected text inside a table must not silently disappear.
				return raw
			}
		}
	}
	if tableOpen || !tableSeen || rowIndex < 0 || columns == 0 {
		return raw
	}
	rows := make([][]string, rowIndex+1)
	for y := range rows {
		rows[y] = make([]string, columns)
		for x := range rows[y] {
			rows[y][x] = grid[[2]int{y, x}]
		}
	}
	table, err := documentTable(rows)
	if err != nil {
		return raw
	}
	parts := []string{}
	if text := strings.TrimSpace(before.String()); text != "" {
		parts = append(parts, text)
	}
	parts = append(parts, table)
	if text := strings.TrimSpace(after.String()); text != "" {
		parts = append(parts, text)
	}
	result := strings.Join(parts, "\n\n")
	if len(result) > maxExtractedText {
		return raw
	}
	return result
}
