package app

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	excel "github.com/jloor/go-excel-reader"
	"github.com/xuri/excelize/v2"
)

func spreadsheetText(data []byte, ext string) (nativeDocument, error) {
	out := nativeDocument{}
	add := func(name string, rows [][]string) error {
		table, err := documentTable(rows)
		if err != nil {
			return err
		}
		if table == "" {
			return nil
		}
		if len(out.Text)+len(table)+len(name)+8 > maxExtractedText {
			return errors.New("Spreadsheet text exceeds the 4 MB document limit")
		}
		if out.Text != "" {
			out.Text += "\n\n"
		}
		out.Text += "## " + name + "\n\n" + table
		out.Tables = append(out.Tables, table)
		return nil
	}
	appendRow := func(rows *[][]string, row []string, size *int) error {
		nonempty := false
		for _, cell := range row {
			*size += len(cell) + 3
			nonempty = nonempty || strings.TrimSpace(cell) != ""
		}
		if *size > maxExtractedText || len(*rows) >= 100000 {
			return errors.New("Spreadsheet exceeds the text or row limit")
		}
		if nonempty {
			*rows = append(*rows, row)
		}
		return nil
	}
	if ext == ".xlsx" {
		if _, err := openDocumentArchive(data); err != nil {
			return out, err
		}
		wb, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 250 << 20, UnzipXMLSizeLimit: 32 << 20})
		if err != nil {
			return out, err
		}
		defer wb.Close()
		for _, name := range wb.GetSheetList() {
			iterator, err := wb.Rows(name)
			if err != nil {
				return out, err
			}
			rows := [][]string{}
			size, scanned := 0, 0
			for iterator.Next() {
				scanned++
				if scanned > 100000 {
					iterator.Close()
					return out, errors.New("Spreadsheet exceeds the 100,000 row limit")
				}
				row, err := iterator.Columns()
				if err == nil {
					err = appendRow(&rows, row, &size)
				}
				if err != nil {
					iterator.Close()
					return out, err
				}
			}
			err = iterator.Error()
			iterator.Close()
			if err != nil {
				return out, err
			}
			if err = add(name, rows); err != nil {
				return out, err
			}
		}
	} else {
		if handled, err := legacySpreadsheet(data, add); handled || err != nil {
			return out, err
		}
		wb, err := excel.OpenXLS(bytes.NewReader(data), excel.WithErrorValues(true))
		if err != nil {
			return out, err
		}
		defer wb.Close()
		for wb.NextSheet() {
			rows := [][]string{}
			size := 0
			for wb.Read() {
				if wb.FieldCount() > 16384 || wb.RowIndex() > 100000 {
					return out, errors.New("Spreadsheet exceeds the row or column limit")
				}
				row := make([]string, wb.FieldCount())
				for i := range row {
					switch v := wb.GetValue(i).(type) {
					case nil:
					case time.Time:
						row[i] = v.Format("2006-01-02 15:04:05")
					case bool:
						if v {
							row[i] = "TRUE"
						} else {
							row[i] = "FALSE"
						}
					default:
						row[i] = fmt.Sprint(v)
					}
				}
				if err = appendRow(&rows, row, &size); err != nil {
					return out, err
				}
			}
			if err = wb.Err(); err != nil {
				return out, err
			}
			if err = add(wb.SheetName(), rows); err != nil {
				return out, err
			}
		}
		if err = wb.Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}

func openDocumentText(data []byte) (nativeDocument, error) {
	out := nativeDocument{}
	z, err := openDocumentArchive(data)
	if err != nil {
		return out, err
	}
	raw, err := z.read("content.xml")
	if err != nil {
		return out, err
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	depth, paragraph := 0, 0
	var b strings.Builder
	parts := []string{}
	const ns = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			depth++
			if depth > 256 {
				return out, errors.New("Document XML is nested too deeply")
			}
			if v.Name.Space == ns {
				switch v.Name.Local {
				case "p", "h":
					if paragraph == 0 {
						paragraph = depth
					}
				case "tab":
					if paragraph > 0 {
						b.WriteByte('\t')
					}
				case "line-break":
					if paragraph > 0 {
						b.WriteByte('\n')
					}
				case "s":
					if paragraph > 0 {
						count := 1
						for _, a := range v.Attr {
							if a.Name.Local == "c" {
								count, _ = strconv.Atoi(a.Value)
							}
						}
						if count < 1 || count > 10000 {
							return out, errors.New("Invalid OpenDocument spacing")
						}
						b.WriteString(strings.Repeat(" ", count))
					}
				}
			}
		case xml.EndElement:
			if paragraph == depth {
				if text := strings.TrimSpace(b.String()); text != "" {
					parts = append(parts, text)
				}
				b.Reset()
				paragraph = 0
			}
			depth--
		case xml.CharData:
			if paragraph > 0 {
				b.Write(v)
			}
		case xml.Directive:
			return out, errors.New("XML directives are not supported")
		}
	}
	out.Text = strings.Join(parts, "\n\n")
	return out, nil
}

func archiveXML(z documentArchive, name string) (*wordNode, error) {
	raw, err := z.read(name)
	if err != nil {
		return nil, err
	}
	return readWordXML(raw)
}

type officeRelationship struct{ ID, Type, Target string }

func officeRelationships(z documentArchive, filename string) ([]officeRelationship, error) {
	rels := path.Join(path.Dir(filename), "_rels", path.Base(filename)+".rels")
	if z[rels] == nil {
		return nil, nil
	}
	n, err := archiveXML(z, rels)
	if err != nil {
		return nil, err
	}
	out := []officeRelationship{}
	for _, root := range n.Children {
		for _, node := range root.Children {
			if node.Name.Local != "Relationship" {
				continue
			}
			rel := officeRelationship{}
			external := false
			for _, a := range node.Attrs {
				switch a.Name.Local {
				case "Id":
					rel.ID = a.Value
				case "Type":
					rel.Type = a.Value
				case "Target":
					rel.Target = a.Value
				case "TargetMode":
					external = a.Value == "External"
				}
			}
			if external {
				continue
			}
			if strings.HasPrefix(rel.Target, "/") {
				rel.Target = path.Clean(strings.TrimPrefix(rel.Target, "/"))
			} else {
				rel.Target = path.Clean(path.Join(path.Dir(filename), rel.Target))
			}
			if strings.HasPrefix(rel.Target, "../") {
				return nil, errors.New("Invalid Office relationship")
			}
			out = append(out, rel)
		}
	}
	return out, nil
}
func drawingText(n *wordNode) string {
	var b strings.Builder
	var walk func(*wordNode)
	walk = func(n *wordNode) {
		if n.Name.Local == "t" {
			b.WriteString(n.Text)
		} else if n.Name.Local == "br" {
			b.WriteByte('\n')
		}
		for _, c := range n.Children {
			walk(c)
		}
		if n.Name.Local == "p" {
			b.WriteByte('\n')
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}
func presentationText(data []byte) (nativeDocument, error) {
	out := nativeDocument{}
	z, err := openDocumentArchive(data)
	if err != nil {
		return out, err
	}
	presentation, err := archiveXML(z, "ppt/presentation.xml")
	if err != nil {
		return out, err
	}
	rels, err := officeRelationships(z, "ppt/presentation.xml")
	if err != nil {
		return out, err
	}
	byID := map[string]string{}
	for _, rel := range rels {
		if strings.HasSuffix(rel.Type, "/slide") {
			byID[rel.ID] = rel.Target
		}
	}
	slides := []string{}
	walkWord(presentation, func(n *wordNode) {
		if n.Name.Local == "sldId" {
			for _, a := range n.Attrs {
				if a.Name.Local == "id" && a.Name.Space != "" {
					slides = append(slides, byID[a.Value])
				}
			}
		}
	})
	if len(slides) > 10000 {
		return out, errors.New("Presentation exceeds the slide limit")
	}
	for index, filename := range slides {
		n, err := archiveXML(z, filename)
		if err != nil {
			return out, err
		}
		parts := []string{fmt.Sprintf("## Slide %d", index+1)}
		var shape func(*wordNode) error
		shape = func(n *wordNode) error {
			switch n.Name.Local {
			case "tbl":
				rows := [][]string{}
				for _, tr := range n.Children {
					if tr.Name.Local != "tr" {
						continue
					}
					row := []string{}
					for _, tc := range tr.Children {
						if tc.Name.Local == "tc" {
							row = append(row, drawingText(tc))
						}
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
				}
				return nil
			case "txBody":
				if text := drawingText(n); text != "" {
					parts = append(parts, text)
				}
				return nil
			}
			for _, child := range n.Children {
				if err := shape(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := shape(n); err != nil {
			return out, err
		}
		rels, err := officeRelationships(z, filename)
		if err != nil {
			return out, err
		}
		for _, rel := range rels {
			if !strings.HasSuffix(rel.Type, "/notesSlide") {
				continue
			}
			notes, err := archiveXML(z, rel.Target)
			if err != nil {
				return out, err
			}
			walkWord(notes, func(n *wordNode) {
				if n.Name.Local != "sp" {
					return
				}
				body := false
				walkWord(n, func(c *wordNode) {
					if c.Name.Local == "ph" {
						for _, a := range c.Attrs {
							if a.Name.Local == "type" && a.Value == "body" {
								body = true
							}
						}
					}
				})
				if body {
					if text := drawingText(n); text != "" {
						parts = append(parts, "Notes: "+text)
					}
				}
			})
		}
		text := ""
		if len(parts) > 1 {
			text = strings.Join(parts, "\n\n")
		}
		out.Pages = append(out.Pages, text)
		if len(out.Text)+len(text)+2 > maxExtractedText {
			return out, errors.New("Presentation text exceeds the 4 MB document limit")
		}
		if text != "" {
			if out.Text != "" {
				out.Text += "\n\n"
			}
			out.Text += text
		}
	}
	return out, nil
}
