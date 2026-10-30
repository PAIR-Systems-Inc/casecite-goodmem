package app

import (
	"bytes"
	"encoding/json"
	"net/mail"
	"path/filepath"
	"strings"
)

// Validate the file itself before sending it to GoodMem, not just its name or
// client-supplied MIME type. Original bytes remain stored only in GoodMem.
func validateDocumentUpload(data []byte, name string) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	limit := 25 << 20
	if ext == ".pdf" {
		limit = 50 << 20
	}
	if strings.Contains("|.txt|.md|.markdown|.csv|.tsv|.html|.htm|.json|", "|"+ext+"|") {
		limit = 10 << 20
	}
	if len(data) > limit {
		return "", fail(413, "File exceeds the size limit for this document type")
	}
	textTypes := map[string]string{".txt": "text/plain", ".md": "text/markdown", ".markdown": "text/markdown", ".csv": "text/csv", ".tsv": "text/tab-separated-values", ".html": "text/html", ".htm": "text/html", ".json": "application/json", ".eml": "message/rfc822", ".rtf": "application/rtf"}
	if ct, ok := textTypes[ext]; ok {
		decoded, err := decodeDocumentText(data)
		if err != nil {
			return "", fail(415, "Text document contains invalid encoding or binary data")
		}
		if ext == ".json" && !json.Valid([]byte(decoded)) {
			return "", fail(415, "Invalid JSON document")
		}
		if ext == ".rtf" && !bytes.HasPrefix(bytes.TrimSpace(data), []byte(`{\rtf`)) {
			return "", fail(415, "Invalid RTF document")
		}
		if ext == ".eml" {
			if _, err := mail.ReadMessage(bytes.NewReader(data)); err != nil {
				return "", fail(415, "Invalid email document")
			}
		}
		return ct, nil
	}
	// Word sometimes saves RTF or OOXML with a .doc name. The original reader
	// supports these; validate the actual container before accepting them.
	if ext == ".doc" && bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return validateDocumentUpload(data, "document.docx")
	}
	if ext == ".doc" && bytes.HasPrefix(bytes.TrimSpace(data), []byte(`{\rtf`)) {
		return validateDocumentUpload(data, "document.rtf")
	}
	if ext == ".pdf" {
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-")) {
			return "", fail(415, "Invalid PDF document")
		}
		return "application/pdf", nil
	}
	archiveTypes := map[string][2]string{".docx": {"word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, ".xlsx": {"xl/workbook.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}, ".pptx": {"ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation"}, ".odt": {"content.xml", "application/vnd.oasis.opendocument.text"}}
	if spec, ok := archiveTypes[ext]; ok {
		z, err := openDocumentArchive(data)
		if err != nil {
			return "", fail(415, "Invalid Office document")
		}
		if z[spec[0]] == nil {
			return "", fail(415, "Office document content does not match its file extension")
		}
		return spec[1], nil
	}
	legacyTypes := map[string]string{".doc": "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint", ".msg": "application/vnd.ms-outlook"}
	if ct, ok := legacyTypes[ext]; ok {
		if !bytes.HasPrefix(data, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
			if ext == ".xls" {
				if _, err := spreadsheetText(data, ext); err == nil {
					return ct, nil
				}
			}
			return "", fail(415, "Invalid legacy Office document")
		}
		return ct, nil
	}
	return "", fail(415, "Supported files: PDF, Word, Excel, PowerPoint, ODT, TXT, Markdown, CSV, TSV, HTML, JSON, EML, MSG and RTF")
}
