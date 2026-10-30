package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

var inlinePDFImage = regexp.MustCompile(`(?:^|[\x00\t\n\f\r ])BI(?:[\x00\t\n\f\r /])`)

// Inline images are stored in content streams instead of XObject resources.
// Treat oversized/unreadable streams conservatively; never mistake a searchable
// heading for complete coverage of the scanned body below it.
func pdfInlineImages(value pdf.Value, depth int) bool {
	if depth > 12 {
		return true
	}
	if value.Kind() == pdf.Array {
		for i := 0; i < value.Len(); i++ {
			if pdfInlineImages(value.Index(i), depth+1) {
				return true
			}
		}
		return false
	}
	if value.Kind() != pdf.Stream {
		return false
	}
	r := value.Reader()
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, (8<<20)+1))
	return err != nil || len(b) > 8<<20 || inlinePDFImage.Match(b)
}

func pdfImageResources(value pdf.Value, depth int) bool {
	if depth > 12 {
		return true
	}
	xobjects := value.Key("XObject")
	for _, key := range xobjects.Keys() {
		x := xobjects.Key(key)
		if x.Key("Subtype").Name() == "Image" {
			return true
		}
		if x.Key("Subtype").Name() == "Form" && (pdfImageResources(x.Key("Resources"), depth+1) || pdfInlineImages(x, 0)) {
			return true
		}
	}
	return false
}
func nativePDFPages(content []byte) (pages []textPage, err error) {
	defer func() {
		if recover() != nil {
			pages = nil
			err = errors.New("PDF text reader could not decode this file")
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}
	if r.NumPage() < 1 || r.NumPage() > 10000 {
		return nil, errors.New("unsupported PDF page count")
	}
	total := 0
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		text, e := page.GetPlainText(nil)
		total += len(text)
		if total > 4<<20 {
			return nil, fail(422, "Extracted text exceeds the 4 MB document limit")
		}
		resources := page.V.Key("Resources")
		parent := page.V
		for depth := 0; resources.IsNull() && depth < 32; depth++ {
			parent = parent.Key("Parent")
			if parent.IsNull() {
				break
			}
			resources = parent.Key("Resources")
		}
		// Image-bearing pages may mix searchable headers with scanned body text.
		// Match the original reader's 50-character sparse-page threshold, and
		// cover image-bearing pages even when their searchable text is longer.
		needsOCR := e != nil || !utf8.ValidString(text) || len([]rune(strings.TrimSpace(text))) < 50 || pdfImageResources(resources, 0) || pdfInlineImages(page.V.Key("Contents"), 0)
		method := "native"
		if needsOCR {
			method = "goodmem_ocr"
			text = ""
		}
		pages = append(pages, textPage{Index: i - 1, Text: text, Method: method, Failed: needsOCR})
	}
	return pages, nil
}
func ocrInputFormat(content []byte) string {
	if bytes.HasPrefix(content, []byte("%PDF-")) {
		return "PDF"
	}
	switch http.DetectContentType(content) {
	case "image/png":
		return "PNG"
	case "image/jpeg":
		return "JPEG"
	case "image/bmp", "image/x-ms-bmp":
		return "BMP"
	case "image/tiff":
		return "TIFF"
	}
	if bytes.HasPrefix(content, []byte{'I', 'I', 42, 0}) || bytes.HasPrefix(content, []byte{'M', 'M', 0, 42}) {
		return "TIFF"
	}
	return ""
}
func (s *Server) cachedDocumentText(ctx context.Context, doc M) (extractedDocument, error) {
	var raw []byte
	var out extractedDocument
	err := s.db.QueryRowContext(ctx, `SELECT data FROM cc_document_text WHERE document_id=$1 AND memory_id=$2 AND reader_version=1 AND data IS NOT NULL`, str(doc, "id"), str(doc, "memory_id")).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}
func (s *Server) readDocumentText(ctx context.Context, doc M) (extractedDocument, error) {
	// GoodMem memory content is immutable. Reindexing assigns a new memory ID,
	// invalidating the cached OCR. The hash binds partial retries to exact bytes.
	cached, err := s.cachedDocumentText(ctx, doc)
	if err == nil && cached.Complete {
		return cached, nil
	}
	if err != nil && !notFound(err) {
		return extractedDocument{}, err
	}
	content, err := s.gm.Content(ctx, str(doc, "memory_id"))
	if err != nil {
		return extractedDocument{}, err
	}
	format := ocrInputFormat(content)
	native := []textPage{}
	if format == "PDF" {
		native, err = nativePDFPages(content)
		if err == nil {
			result := assembleText(native)
			if result.Complete {
				return result, nil
			}
		}
	} else if format == "" {
		text, e := extractText(content, str(doc, "filename"))
		if e == nil && strings.TrimSpace(text) != "" {
			return assembleText([]textPage{{Index: 0, Text: text, Method: "native"}}), nil
		}
	}
	hash := sha256.Sum256(content)
	sha := hex.EncodeToString(hash[:])
	lease := token()
	id, memory := str(doc, "id"), str(doc, "memory_id")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		r, e := s.db.ExecContext(ctx, `INSERT INTO cc_document_text(document_id,memory_id,lease_token,lease_until) VALUES($1,$2,$3,now()+interval '45 seconds') ON CONFLICT(document_id) DO UPDATE SET memory_id=$2,reader_version=1,lease_token=$3,lease_until=now()+interval '45 seconds',data=CASE WHEN cc_document_text.memory_id=$2 AND cc_document_text.reader_version=1 THEN cc_document_text.data ELSE NULL END WHERE cc_document_text.lease_until IS NULL OR cc_document_text.lease_until<now() OR cc_document_text.memory_id<>$2`, id, memory, lease)
		if e != nil {
			return extractedDocument{}, e
		}
		if count, _ := r.RowsAffected(); count == 1 {
			break
		}
		select {
		case <-ctx.Done():
			return extractedDocument{}, ctx.Err()
		case <-ticker.C:
		}
		cached, e = s.cachedDocumentText(ctx, doc)
		if e == nil && cached.Complete {
			return cached, nil
		}
		if e != nil && !notFound(e) {
			return extractedDocument{}, e
		}
	}
	// Renew while GoodMem renders/OCRs. No database transaction or connection
	// stays open across network calls; another app instance sees the same lease.
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-done:
				return
			case <-work.Done():
				return
			case <-heartbeat.C:
				r, e := s.db.ExecContext(work, `UPDATE cc_document_text SET lease_until=now()+interval '45 seconds' WHERE document_id=$1 AND memory_id=$2 AND lease_token=$3`, id, memory, lease)
				if e != nil {
					cancel()
					return
				}
				if n, _ := r.RowsAffected(); n != 1 {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(done)
		<-stopped
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.db.ExecContext(cleanup, `UPDATE cc_document_text SET lease_token=NULL,lease_until=NULL WHERE document_id=$1 AND memory_id=$2 AND lease_token=$3`, id, memory, lease)
	}()
	// A predecessor may have finished between our initial cache read and claim.
	cached, e := s.cachedDocumentText(work, doc)
	if e == nil && cached.Complete {
		return cached, nil
	}
	if e != nil && !notFound(e) {
		return extractedDocument{}, e
	}
	if cached.SHA256 != sha {
		cached = extractedDocument{}
	}
	if s.ocr != nil {
		select {
		case s.ocr <- struct{}{}:
			defer func() { <-s.ocr }()
		case <-work.Done():
			return extractedDocument{}, work.Err()
		}
	}
	if job, ok := work.Value(jobLeaseKey{}).(jobLease); ok {
		s.jobProgress(work, job.ID, "Reading document pages with GoodMem OCR…", 0.1)
	}
	checkpoint := func(result extractedDocument) error {
		result.SHA256 = sha
		return s.saveDocumentText(work, id, memory, lease, result)
	}
	var result extractedDocument
	switch {
	case format == "PDF" && len(native) > 0:
		for _, page := range cached.Pages {
			if page.Index >= 0 && page.Index < len(native) && !page.Failed && native[page.Index].Failed {
				native[page.Index] = page
			}
		}
		// OCR consecutive missing ranges, up to ten pages per request. Successful
		// pages from a partial attempt survive an explicit retry.
		for first := 0; first < len(native); {
			if !native[first].Failed {
				first++
				continue
			}
			last := first
			for last+1 < len(native) && last-first < 9 && native[last+1].Failed {
				last++
			}
			part, e := s.gm.OCR(work, content, "PDF", &first, &last)
			if e != nil {
				err = e
				break
			}
			for _, page := range part.Pages {
				native[page.Index] = page
			}
			// Save each completed range, so cancellation of a later request
			// does not discard pages that have already consumed OCR work.
			if e = checkpoint(assembleText(native)); e != nil {
				return extractedDocument{}, e
			}
			first = last + 1
		}
		result = assembleText(native)
	case format != "":
		result, err = s.gm.OCR(work, content, format, nil, nil)
	default:
		result, err = s.ocrRenderedPages(work, doc, cached, checkpoint)
	}
	if errors.Is(err, errNoDocumentImages) {
		return result, fail(422, "GoodMem page images are not available for this file. Check its page-image processing status before requesting full text.")
	}
	if len(result.Text) > 4<<20 {
		return extractedDocument{}, fail(422, "Extracted text exceeds the 4 MB document limit")
	}
	if len(result.Pages) > 0 {
		result.SHA256 = sha
		if e := s.saveDocumentText(work, id, memory, lease, result); e != nil {
			return result, e
		}
	}
	return result, err
}
func (s *Server) saveDocumentText(ctx context.Context, id, memory, lease string, result extractedDocument) error {
	if len(result.Text) > 4<<20 {
		return fail(422, "Extracted text exceeds the 4 MB document limit")
	}
	r, err := s.db.ExecContext(ctx, `UPDATE cc_document_text SET data=$4,updated_at=now() WHERE document_id=$1 AND memory_id=$2 AND lease_token=$3 AND EXISTS(SELECT 1 FROM cc_objects WHERE kind='document' AND id=$1 AND data->>'memory_id'=$2)`, id, memory, lease, jsonBytes(result))
	if err != nil {
		return err
	}
	if count, _ := r.RowsAffected(); count != 1 {
		return fail(409, "Document changed or was deleted during OCR. Reopen the document.")
	}
	return nil
}
func (s *Server) ocrRenderedPages(ctx context.Context, doc M, cached extractedDocument, checkpoint func(extractedDocument) error) (extractedDocument, error) {
	memory := str(doc, "memory_id")
	meta, err := s.gm.Memory(ctx, memory)
	if err != nil {
		return extractedDocument{}, err
	}
	if str(meta, "pageImageStatus") != "COMPLETED" {
		return extractedDocument{}, errNoDocumentImages
	}
	images, err := s.gm.Pages(ctx, memory)
	if err != nil {
		return extractedDocument{}, err
	}
	byIndex := map[int]M{}
	last := -1
	for _, image := range images {
		index := number(image, "pageIndex", -1)
		if index < 0 || index > 9999 {
			return extractedDocument{}, fail(502, "GoodMem returned invalid page-image indices")
		}
		if prev, ok := byIndex[index]; !ok || number(image, "dpi", 0) > number(prev, "dpi", 0) {
			byIndex[index] = image
		}
		last = max(last, index)
	}
	if last < 0 {
		return extractedDocument{}, errNoDocumentImages
	}
	pages := make([]textPage, last+1)
	for i := range pages {
		pages[i] = textPage{Index: i, Method: "goodmem_ocr", Failed: true}
	}
	for _, page := range cached.Pages {
		if page.Index >= 0 && page.Index < len(pages) && !page.Failed {
			pages[page.Index] = page
		}
	}
	for i := range pages {
		if !pages[i].Failed {
			continue
		}
		image, ok := byIndex[i]
		if !ok {
			continue
		}
		params := url.Values{}
		if dpi := number(image, "dpi", 0); dpi > 0 {
			params.Set("dpi", strconv.Itoa(dpi))
		}
		params.Set("contentType", str(image, "contentType"))
		resp, e := s.gm.Page(ctx, memory, strconv.Itoa(i), params)
		if e != nil {
			return assembleText(pages), e
		}
		content, e := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
		resp.Body.Close()
		if e != nil {
			return assembleText(pages), e
		}
		if len(content) > 32<<20 {
			return assembleText(pages), fail(422, "GoodMem page image exceeds the 32 MB limit")
		}
		part, e := s.gm.OCR(ctx, content, "AUTO", nil, nil)
		if e != nil {
			return assembleText(pages), e
		}
		if len(part.Pages) != 1 {
			return assembleText(pages), fmt.Errorf("expected one OCR page for one rendered image")
		}
		pages[i] = part.Pages[0]
		pages[i].Index = i
		if err := checkpoint(assembleText(pages)); err != nil {
			return assembleText(pages), err
		}
	}
	return assembleText(pages), nil
}
