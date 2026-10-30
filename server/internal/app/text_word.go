package app

import "strings"

// The upload extraction/preview contract preserves interleaved Word tables and
// headings. Canonical redline text continues to use wordVisibleText so existing
// UTF-16 edit anchors do not shift when presentation formatting changes.
func wordExtraction(data []byte) (nativeDocument, error) {
	out := nativeDocument{}
	z, err := openDocumentArchive(data)
	if err != nil {
		return out, err
	}
	root, err := archiveXML(z, "word/document.xml")
	if err != nil {
		return out, err
	}
	var body *wordNode
	walkWord(root, func(n *wordNode) {
		if isWord(n, "body") {
			body = n
		}
	})
	if body == nil {
		return out, bad("Word document body is missing")
	}
	parts := []string{}
	var visit func(*wordNode) error
	visit = func(n *wordNode) error {
		switch {
		case isWord(n, "del"), isWord(n, "moveFrom"):
			return nil
		case isWord(n, "p"):
			text := strings.TrimSpace(wordVisibleText(n, nil, 0))
			if text == "" {
				return nil
			}
			kind := "text"
			walkWord(n, func(c *wordNode) {
				if isWord(c, "pStyle") {
					for _, a := range c.Attrs {
						v := strings.ToLower(a.Value)
						if a.Name.Local == "val" && (strings.Contains(v, "heading") || strings.Contains(v, "title")) {
							kind = "heading"
						}
					}
				}
			})
			parts = append(parts, text)
			out.Blocks = append(out.Blocks, reportBlock{Kind: kind, Text: text})
			return nil
		case isWord(n, "tbl"):
			rows := [][]string{}
			for _, tr := range n.Children {
				if !isWord(tr, "tr") {
					continue
				}
				row := []string{}
				for _, tc := range tr.Children {
					if !isWord(tc, "tc") {
						continue
					}
					row = append(row, strings.TrimSpace(wordVisibleText(tc, nil, 0)))
				}
				rows = append(rows, row)
			}
			table, err := documentTable(rows)
			if err != nil {
				return err
			}
			if table != "" {
				parts = append(parts, table)
				out.Tables = append(out.Tables, table)
				out.Blocks = append(out.Blocks, reportBlock{Kind: "text", Text: table, Rows: rows})
			}
			return nil
		}
		for _, c := range n.Children {
			if err := visit(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err = visit(body); err != nil {
		return out, err
	}
	out.Text = strings.Join(parts, "\n\n")
	return out, nil
}
