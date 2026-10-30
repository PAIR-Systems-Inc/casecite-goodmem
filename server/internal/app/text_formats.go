package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/charmap"
)

const maxExtractedText = 4 << 20

type nativeDocument struct {
	Text   string
	Tables []string
	Pages  []string // Only formats with intrinsic page/slide boundaries.
	Blocks []reportBlock
}

func extractNativeDocument(data []byte, filename string) (out nativeDocument, err error) {
	defer func() {
		if recover() != nil {
			out = nativeDocument{}
			err = errors.New("The document is truncated or corrupt")
		}
		if err == nil && len(out.Text) > maxExtractedText {
			out = nativeDocument{}
			err = errors.New("Extracted text exceeds the 4 MB document limit")
		}
	}()
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".docx":
		return wordExtraction(data)
	case ".doc":
		if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return wordExtraction(data)
		} else if bytes.HasPrefix(bytes.TrimSpace(data), []byte(`{\rtf`)) {
			out.Text, err = rtfText(data)
		} else {
			out.Text, err = legacyWordText(data)
		}
	case ".csv", ".tsv":
		var raw string
		raw, err = decodeDocumentText(data)
		if err != nil || strings.TrimSpace(raw) == "" {
			return out, err
		}
		r := csv.NewReader(strings.NewReader(raw))
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		if strings.EqualFold(filepath.Ext(filename), ".tsv") {
			r.Comma = '\t'
		}
		var rows [][]string
		for {
			row, e := r.Read()
			if e == io.EOF {
				break
			}
			if e != nil {
				return out, e
			}
			rows = append(rows, row)
			if len(rows) > 100000 {
				return out, errors.New("Table exceeds the 100,000 row limit")
			}
		}
		out.Text, err = documentTable(rows)
		if out.Text != "" {
			out.Tables = []string{out.Text}
		}
	case ".xlsx", ".xls":
		return spreadsheetText(data, strings.ToLower(filepath.Ext(filename)))
	case ".pptx":
		return presentationText(data)
	case ".odt":
		return openDocumentText(data)
	case ".html", ".htm":
		var raw string
		raw, err = decodeDocumentText(data)
		if err == nil {
			out.Text, err = documentHTMLText(raw)
		}
	case ".eml":
		out.Text, err = emailText(data)
	case ".msg":
		out.Text, err = outlookText(data)
	case ".rtf":
		out.Text, err = rtfText(data)
	default:
		out.Text, err = decodeDocumentText(data)
	}
	return out, err
}

func decodeUTF16(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("Truncated UTF-16 text")
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units)), nil
}

// Decode common Windows-authored documents without guessing UTF-16 from arbitrary
// binary bytes. A BOM is required, and decoded NULs are always rejected.
func decodeDocumentText(data []byte) (text string, err error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		text, err = decodeUTF16(data[2:], binary.LittleEndian)
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		text, err = decodeUTF16(data[2:], binary.BigEndian)
	default:
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if utf8.Valid(data) {
			text = string(data)
		} else {
			encoding := charmap.Windows1252
			for _, b := range data {
				if b == 0x81 || b == 0x8d || b == 0x8f || b == 0x90 || b == 0x9d {
					encoding = charmap.ISO8859_1
					break
				}
			}
			var decoded []byte
			decoded, err = encoding.NewDecoder().Bytes(data)
			text = string(decoded)
		}
	}
	if strings.ContainsRune(text, 0) {
		return "", errors.New("Text document contains binary data")
	}
	return text, err
}

func documentTable(rows [][]string) (string, error) {
	if len(rows) == 0 {
		return "", nil
	}
	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols > 16384 {
		return "", errors.New("Table exceeds the column limit")
	}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteByte('|')
		for col := 0; col < cols; col++ {
			cell := ""
			if col < len(row) {
				cell = row[col]
			}
			// Escaping literal pipes avoids inventing extra columns, an upstream bug.
			cell = strings.NewReplacer("\\", "\\\\", "|", "\\|", "\r\n", " ", "\n", " ", "\r", " ").Replace(cell)
			b.WriteString(" " + strings.TrimSpace(cell) + " |")
			if b.Len() > maxExtractedText {
				return "", errors.New("Extracted table exceeds the 4 MB document limit")
			}
		}
		if i == 0 {
			b.WriteString("\n|")
			for col := 0; col < cols; col++ {
				b.WriteString(" --- |")
			}
		}
	}
	return b.String(), nil
}

func documentHTMLText(raw string) (string, error) {
	// The upstream skip-depth parser never leaves a void <meta>/<link> element.
	// Tokenization recognizes those as void and preserves the visible body.
	z := html.NewTokenizer(strings.NewReader(raw))
	blocks := map[string]bool{"p": true, "div": true, "br": true, "li": true, "tr": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "section": true, "article": true, "header": true, "footer": true, "table": true, "blockquote": true}
	skip := ""
	var b strings.Builder
	for {
		typeOf := z.Next()
		if typeOf == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", z.Err()
			}
			break
		}
		t := z.Token()
		switch typeOf {
		case html.StartTagToken, html.SelfClosingTagToken:
			if skip == "" && (t.Data == "script" || t.Data == "style" || t.Data == "head" || t.Data == "title") && typeOf != html.SelfClosingTagToken {
				skip = t.Data
			} else if skip == "" && blocks[t.Data] {
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			if t.Data == skip {
				skip = ""
			} else if skip == "" && blocks[t.Data] {
				b.WriteByte('\n')
			}
		case html.TextToken:
			if skip == "" {
				b.WriteString(t.Data)
			}
		}
		if b.Len() > maxExtractedText {
			return "", errors.New("HTML text exceeds the 4 MB document limit")
		}
	}
	return nonemptyDocumentLines(b.String(), "\n"), nil
}

func nonemptyDocumentLines(text, separator string) string {
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, separator)
}

type documentArchive map[string]*zip.File

func openDocumentArchive(data []byte) (documentArchive, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(z.File) > 10000 {
		return nil, errors.New("Document archive has too many entries")
	}
	files := documentArchive{}
	var total uint64
	for _, f := range z.File {
		if f.UncompressedSize64 > 250<<20 || total > (250<<20)-f.UncompressedSize64 {
			return nil, errors.New("Expanded document exceeds the 250 MB limit")
		}
		total += f.UncompressedSize64
		if files[f.Name] != nil {
			return nil, errors.New("Document archive contains duplicate entries")
		}
		files[f.Name] = f
	}
	return files, nil
}
func (z documentArchive) read(name string) ([]byte, error) {
	f := z[name]
	if f == nil {
		return nil, fmt.Errorf("Document is missing %s", name)
	}
	if f.UncompressedSize64 > 32<<20 {
		return nil, errors.New("Document XML exceeds the 32 MB limit")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, (32<<20)+1))
	if len(b) > 32<<20 {
		return nil, errors.New("Document XML exceeds the 32 MB limit")
	}
	return b, err
}
