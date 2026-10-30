package app

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Full-document features read the original stored in GoodMem. These lightweight
// readers do not create another search index and are not part of ingestion.
func extractText(data []byte, filename string) (text string, err error) {
	defer func() {
		if recover() != nil {
			text = ""
			err = errors.New("the document reader could not decode this file")
		}
	}()
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", err
		}
		reader, err := r.GetPlainText()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
		if len(b) > 4<<20 {
			return "", errors.New("extracted text exceeds the 4 MB limit")
		}
		return string(b), err
	}
	if strings.EqualFold(filepath.Ext(filename), ".docx") {
		z, err := openDocumentArchive(data)
		if err != nil {
			return "", err
		}
		for _, f := range z {
			if f.Name != "word/document.xml" {
				continue
			}
			if f.UncompressedSize64 > 16<<20 {
				return "", errors.New("DOCX text exceeds the 16 MB limit")
			}
			r, err := f.Open()
			if err != nil {
				return "", err
			}
			defer r.Close()
			raw, err := io.ReadAll(io.LimitReader(r, (16<<20)+1))
			if err != nil {
				return "", err
			}
			if len(raw) > 16<<20 {
				return "", errors.New("DOCX text exceeds the 16 MB limit")
			}
			root, err := readWordXML(raw)
			if err != nil {
				return "", err
			}
			return wordVisibleText(root, nil, 0), nil
		}
		return "", errors.New("DOCX document body was not found")
	}
	result, err := extractNativeDocument(data, filename)
	return result.Text, err
}

func wordDocument(title, text string) ([]byte, error) {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	content := text
	if title != "" {
		content = title + "\n\n" + text
	}
	for _, line := range strings.Split(content, "\n") {
		var esc bytes.Buffer
		_ = xml.EscapeText(&esc, []byte(line))
		body.WriteString(`<w:p><w:r><w:t xml:space="preserve">` + esc.String() + `</w:t></w:r></w:p>`)
	}
	body.WriteString(`<w:sectPr/></w:body></w:document>`)
	files["word/document.xml"] = body.String()
	for name, content := range files {
		w, err := z.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = w.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
