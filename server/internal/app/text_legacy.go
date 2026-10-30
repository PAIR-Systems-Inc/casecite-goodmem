package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/richardlehane/mscfb"
	"golang.org/x/text/encoding/charmap"
)

func compoundDocument(data []byte) (map[string]*mscfb.File, error) {
	r, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("Invalid legacy Office container")
	}
	streams := map[string]*mscfb.File{}
	for _, f := range r.File {
		if len(streams) >= 10000 {
			return nil, errors.New("Legacy document has too many streams")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.Join(append(append([]string{}, f.Path...), f.Name), "/")
		if streams[name] != nil {
			return nil, errors.New("Legacy document contains duplicate streams")
		}
		streams[name] = f
	}
	if streams["EncryptedPackage"] != nil {
		return nil, errors.New("Document is password-protected; remove the password and upload it again")
	}
	return streams, nil
}
func compoundBytes(f *mscfb.File) ([]byte, error) {
	if f == nil {
		return nil, errors.New("Required legacy document stream is missing")
	}
	if f.Size < 0 || f.Size > 32<<20 {
		return nil, errors.New("Legacy document stream exceeds the 32 MB limit")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if len(b) > 32<<20 {
		return nil, errors.New("Legacy document stream exceeds the 32 MB limit")
	}
	return b, err
}
func legacyWordText(data []byte) (string, error) {
	streams, err := compoundDocument(data)
	if err != nil {
		return "", err
	}
	word, err := compoundBytes(streams["WordDocument"])
	if err != nil {
		return "", err
	}
	if len(word) < 0x1aa {
		return "", errors.New("Word document is truncated or corrupt")
	}
	flags := binary.LittleEndian.Uint16(word[0x0a:])
	if flags&0x0100 != 0 {
		return "", errors.New("Document is password-protected; remove the password and upload it again")
	}
	name := "0Table"
	if flags&0x0200 != 0 {
		name = "1Table"
	}
	table, err := compoundBytes(streams[name])
	if err != nil {
		return "", err
	}
	start, size := int(binary.LittleEndian.Uint32(word[0x1a2:])), int(binary.LittleEndian.Uint32(word[0x1a6:]))
	if start > len(table) || size > len(table)-start {
		return "", errors.New("Word piece table is truncated")
	}
	text, err := wordPieceText(word, table[start:start+size], int(binary.LittleEndian.Uint32(word[0x4c:])))
	if err != nil {
		return "", err
	}
	return cleanLegacyWordText(text), nil
}
func wordPieceText(word, clx []byte, characters int) (string, error) {
	bad := errors.New("Word document piece table is truncated or corrupt")
	pos := 0
	for pos < len(clx) && clx[pos] == 1 {
		if pos+3 > len(clx) {
			return "", bad
		}
		pos += 3 + int(binary.LittleEndian.Uint16(clx[pos+1:]))
	}
	if pos+5 > len(clx) || clx[pos] != 2 {
		return "", bad
	}
	size := int(binary.LittleEndian.Uint32(clx[pos+1:]))
	if size < 16 || (size-4)%12 != 0 || size > len(clx)-(pos+5) {
		return "", bad
	}
	plc := clx[pos+5 : pos+5+size]
	count := (len(plc) - 4) / 12
	var out strings.Builder
	for i := 0; i < count; i++ {
		start, end := int(binary.LittleEndian.Uint32(plc[i*4:])), int(binary.LittleEndian.Uint32(plc[(i+1)*4:]))
		if end < start {
			return "", bad
		}
		if characters > 0 {
			end = min(end, characters)
		}
		if end <= start {
			continue
		}
		fc := binary.LittleEndian.Uint32(plc[(count+1)*4+i*8+2:])
		offset := int(fc & 0x3fffffff)
		length := end - start
		compressed := fc&0x40000000 != 0
		if compressed {
			offset /= 2
		} else {
			length *= 2
		}
		if offset > len(word) || length > len(word)-offset {
			return "", bad
		}
		var text string
		var err error
		if compressed {
			var b []byte
			b, err = charmap.Windows1252.NewDecoder().Bytes(word[offset : offset+length])
			text = string(b)
		} else {
			text, err = decodeUTF16(word[offset:offset+length], binary.LittleEndian)
		}
		if err != nil {
			return "", err
		}
		if out.Len()+len(text) > maxExtractedText {
			return "", errors.New("Word text exceeds the 4 MB document limit")
		}
		out.WriteString(text)
	}
	return out.String(), nil
}
func cleanLegacyWordText(text string) string {
	out := []rune{}
	fields := []bool{}
	instructions := 0
	for _, r := range text {
		switch r {
		case 0x13:
			fields = append(fields, true)
			instructions++
		case 0x14:
			if len(fields) > 0 && fields[len(fields)-1] {
				fields[len(fields)-1] = false
				instructions--
			}
		case 0x15:
			if len(fields) > 0 {
				if fields[len(fields)-1] {
					instructions--
				}
				fields = fields[:len(fields)-1]
			}
		default:
			if instructions > 0 {
				continue
			}
			switch r {
			case '\r', '\v', '\f':
				out = append(out, '\n')
			case 7:
				if len(out) > 0 && out[len(out)-1] == '\t' {
					out[len(out)-1] = '\n'
				} else {
					out = append(out, '\t')
				}
			case 0x1e:
				out = append(out, '-')
			default:
				if r == '\t' || r >= ' ' {
					out = append(out, r)
				}
			}
		}
	}
	return nonemptyDocumentLines(string(out), "\n")
}

func outlookText(data []byte) (string, error) {
	streams, err := compoundDocument(data)
	if err != nil {
		return "", err
	}
	readString := func(property, storage string) (string, error) {
		for _, suffix := range []string{"001F", "001E"} {
			f := streams[storage+"__substg1.0_"+property+suffix]
			if f == nil {
				continue
			}
			b, err := compoundBytes(f)
			if err != nil {
				return "", err
			}
			text := ""
			if suffix == "001F" {
				text, err = decodeUTF16(b, binary.LittleEndian)
			} else {
				b, err = charmap.Windows1252.NewDecoder().Bytes(b)
				text = string(b)
			}
			return strings.TrimSpace(strings.Trim(text, "\x00")), err
		}
		return "", nil
	}
	props := map[string]string{}
	for _, name := range []string{"0C1A", "5D01", "0C1F", "0E04", "0E03", "0037", "1000"} {
		props[name], err = readString(name, "")
		if err != nil {
			return "", err
		}
	}
	sender, address := props["0C1A"], props["5D01"]
	if address == "" {
		address = props["0C1F"]
	}
	if sender != "" && address != "" {
		sender += " <" + address + ">"
	} else if sender == "" {
		sender = address
	}
	date := ""
	if f := streams["__properties_version1.0"]; f != nil {
		b, err := compoundBytes(f)
		if err != nil {
			return "", err
		}
		for i := 32; i+16 <= len(b); i += 16 {
			tag := binary.LittleEndian.Uint32(b[i:])
			if tag != 0x00390040 && tag != 0x0e060040 {
				continue
			}
			ft := binary.LittleEndian.Uint64(b[i+8:])
			if ft == 0 {
				continue
			}
			seconds := int64(ft/10000000) - 11644473600
			t := time.Unix(seconds, int64(ft%10000000)*100).UTC()
			if t.Year() < 1601 || t.Year() > 9999 {
				return "", errors.New("Invalid Outlook message timestamp")
			}
			date = t.Format("2006-01-02T15:04:05.999999-07:00")
			break
		}
	}
	parts := []string{}
	for _, pair := range [][2]string{{"From", sender}, {"To", props["0E04"]}, {"Cc", props["0E03"]}, {"Date", date}, {"Subject", props["0037"]}} {
		if pair[1] != "" {
			parts = append(parts, pair[0]+": "+pair[1])
		}
	}
	body := props["1000"]
	if body == "" && streams["__substg1.0_10130102"] != nil {
		b, e := compoundBytes(streams["__substg1.0_10130102"])
		if e != nil {
			return "", e
		}
		raw, e := decodeDocumentText(b)
		if e != nil {
			return "", e
		}
		body, err = documentHTMLText(raw)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(body) != "" {
		parts = append(parts, strings.TrimSpace(body))
	}
	storages := map[string]bool{}
	for name := range streams {
		top := strings.SplitN(name, "/", 2)[0]
		if strings.HasPrefix(top, "__attach_version1.0_") {
			storages[top] = true
		}
	}
	keys := []string{}
	for key := range storages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attachments := []string{}
	for _, key := range keys {
		name, err := readString("3707", key+"/")
		if err != nil {
			return "", err
		}
		if name == "" {
			name, err = readString("3704", key+"/")
			if err != nil {
				return "", err
			}
		}
		if name != "" {
			attachments = append(attachments, name)
		}
	}
	if len(attachments) > 0 {
		parts = append(parts, "Attachments: "+strings.Join(attachments, ", "))
	}
	return strings.Join(parts, "\n\n"), nil
}
