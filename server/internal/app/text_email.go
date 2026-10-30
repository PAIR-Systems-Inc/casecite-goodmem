package app

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"

	"golang.org/x/net/html/charset"
)

func emailText(data []byte) (string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("Invalid email message")
	}
	decoder := mime.WordDecoder{CharsetReader: charset.NewReaderLabel}
	parts := []string{}
	for _, name := range []string{"From", "To", "Cc", "Date", "Subject"} {
		if value := message.Header.Get(name); value != "" {
			decoded, err := decoder.DecodeHeader(value)
			if err != nil {
				return "", err
			}
			parts = append(parts, name+": "+decoded)
		}
	}
	attachments := []string{}
	count, total := 0, 0
	type mailBody struct{ plain, html string }
	var readPart func(textproto.MIMEHeader, io.Reader, int) (mailBody, error)
	readPart = func(header textproto.MIMEHeader, raw io.Reader, depth int) (mailBody, error) {
		out := mailBody{}
		count++
		if depth > 30 || count > 1000 {
			return out, errors.New("Email contains too many MIME parts")
		}
		ct := header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		media, params, err := mime.ParseMediaType(ct)
		if err != nil {
			return out, errors.New("Invalid email content type")
		}
		disposition, attributes, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
		filename := attributes["filename"]
		if filename == "" {
			filename = params["name"]
		}
		if disposition == "attachment" || filename != "" {
			if filename != "" {
				filename, err = decoder.DecodeHeader(filename)
				if err != nil {
					return out, err
				}
				attachments = append(attachments, filename)
			}
			return out, nil
		}
		if strings.HasPrefix(media, "multipart/") {
			if params["boundary"] == "" {
				return out, errors.New("Email multipart boundary is missing")
			}
			reader := multipart.NewReader(raw, params["boundary"])
			for {
				part, e := reader.NextRawPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					return out, e
				}
				body, e := readPart(part.Header, part, depth+1)
				part.Close()
				if e != nil {
					return out, e
				}
				if media == "multipart/alternative" {
					if body.plain != "" {
						out.plain = body.plain
					}
					if body.html != "" {
						out.html = body.html
					}
				} else { // Related/mixed bodies keep their own preferred representation.
					text := body.plain
					if text == "" {
						text = body.html
					}
					if text != "" {
						if out.plain != "" {
							out.plain += "\n\n"
						}
						out.plain += text
					}
				}
			}
			return out, nil
		}
		if media != "text/plain" && media != "text/html" {
			return out, nil
		}
		var reader io.Reader = raw
		switch strings.ToLower(header.Get("Content-Transfer-Encoding")) {
		case "base64":
			reader = base64.NewDecoder(base64.StdEncoding, raw)
		case "quoted-printable":
			reader = quotedprintable.NewReader(raw)
		}
		if label := params["charset"]; label != "" {
			reader, err = charset.NewReaderLabel(label, reader)
			if err != nil {
				return out, errors.New("Unsupported email character encoding")
			}
		}
		b, err := io.ReadAll(io.LimitReader(reader, maxExtractedText+1))
		if err != nil {
			return out, err
		}
		total += len(b)
		if total > maxExtractedText {
			return out, errors.New("Email text exceeds the 4 MB document limit")
		}
		text, err := decodeDocumentText(b)
		if err != nil {
			return out, err
		}
		if media == "text/html" {
			out.html, err = documentHTMLText(text)
		} else {
			out.plain = strings.TrimSpace(text)
		}
		return out, err
	}
	body, err := readPart(textproto.MIMEHeader(message.Header), message.Body, 0)
	if err != nil {
		return "", err
	}
	text := body.plain
	if text == "" {
		text = body.html
	}
	if text != "" {
		parts = append(parts, text)
	}
	if len(attachments) > 0 {
		parts = append(parts, "Attachments: "+strings.Join(attachments, ", "))
	}
	return strings.Join(parts, "\n\n"), nil
}
