package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// BIFF2–5 precede the BIFF8 reader's coverage. These formats store cached cell
// values in simple records, either directly or in an OLE Book/Workbook stream.
// We read values only; macros, formula programs and external links never run.
// Record layouts: https://www.openoffice.org/sc/excelfileformat.pdf
type biffReader struct {
	data    []byte
	pos     int
	records *int
}

var errBIFF = errors.New("Truncated or invalid legacy Excel workbook")

func (r *biffReader) next() (uint16, []byte, error) {
	(*r.records)++
	if *r.records > 500000 || r.pos < 0 || len(r.data)-r.pos < 4 {
		return 0, nil, errBIFF
	}
	id := binary.LittleEndian.Uint16(r.data[r.pos:])
	n := int(binary.LittleEndian.Uint16(r.data[r.pos+2:]))
	r.pos += 4
	if n > len(r.data)-r.pos {
		return 0, nil, errBIFF
	}
	data := r.data[r.pos : r.pos+n]
	r.pos += n
	return id, data, nil
}

func biffBOF(id uint16, data []byte) (version int, kind uint16, err error) {
	if len(data) < 4 {
		return 0, 0, errBIFF
	}
	switch id {
	case 0x0009:
		version = 2
	case 0x0209:
		version = 3
	case 0x0409:
		version = 4
	case 0x0809:
		switch binary.LittleEndian.Uint16(data) {
		case 0x0500:
			version = 5 // Excel 5 and 95 use the same cell encodings.
		case 0x0600:
			version = 8
		default:
			return 0, 0, errBIFF
		}
	default:
		return 0, 0, errBIFF
	}
	return version, binary.LittleEndian.Uint16(data[2:]), nil
}

func biffText(r *biffReader, data []byte, offset, width int, enc encoding.Encoding) (string, error) {
	if len(data)-offset < width {
		return "", errBIFF
	}
	n := int(data[offset])
	if width == 2 {
		n = int(binary.LittleEndian.Uint16(data[offset:]))
	}
	data = data[offset+width:]
	text := append([]byte{}, data[:min(n, len(data))]...)
	for len(text) < n {
		if r == nil {
			return "", errBIFF
		}
		id, part, err := r.next()
		if err != nil || id != 0x003c || len(part) == 0 || len(part) > n-len(text) {
			return "", errBIFF
		}
		text = append(text, part...)
	}
	decoded, err := enc.NewDecoder().Bytes(text)
	if err != nil || strings.ContainsRune(string(decoded), '\ufffd') {
		return "", errors.New("Invalid Excel character encoding")
	}
	return strings.TrimSpace(string(decoded)), nil
}

func biffCodepage(data []byte) (encoding.Encoding, error) {
	if len(data) < 2 {
		return nil, errBIFF
	}
	code := int(binary.LittleEndian.Uint16(data))
	if code == 32768 {
		code = 10000
	}
	if code == 32769 {
		code = 1252
	}
	enc := documentCodepage(code)
	if enc == nil {
		return nil, fmt.Errorf("Unsupported Excel code page %d; save as XLSX", code)
	}
	return enc, nil
}

func excelWorkbookStream(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		streams, err := compoundDocument(data)
		if err != nil {
			return nil, err
		}
		stream := streams["Workbook"]
		if stream == nil {
			stream = streams["Book"]
		}
		return compoundBytes(stream)
	}
	return data, nil
}

func excelBIFFVersion(data []byte) (int, error) {
	data, err := excelWorkbookStream(data)
	if err != nil {
		return 0, err
	}
	records := 0
	r := &biffReader{data: data, records: &records}
	id, payload, err := r.next()
	if err != nil {
		return 0, err
	}
	version, _, err := biffBOF(id, payload)
	return version, err
}

func legacySpreadsheet(data []byte, add func(string, [][]string) error) (bool, error) {
	data, err := excelWorkbookStream(data)
	if err != nil {
		return true, err
	}
	records := 0
	r := &biffReader{data: data, records: &records}
	id, payload, err := r.next()
	if err != nil {
		return true, err
	}
	version, kind, err := biffBOF(id, payload)
	if err != nil {
		return true, err
	}
	if version == 8 {
		return false, nil
	}
	enc := encoding.Encoding(charmap.ISO8859_1) // xlrd's fallback without CODEPAGE.
	if kind == 0x0010 {
		rows, err := biffSheet(r, version, enc)
		if err == nil {
			err = add("Sheet 1", rows)
		}
		return true, err
	}
	if kind != 0x0005 && !(version == 4 && kind == 0x0100) {
		return true, errBIFF
	}
	type sheet struct {
		name   string
		offset int
	}
	sheets := []sheet{}
	seen := map[int]bool{}
	for {
		id, payload, err = r.next()
		if err != nil {
			return true, err
		}
		switch id {
		case 0x000a: // End of globals. BIFF4W sheets have already been read.
			for _, s := range sheets {
				if seen[s.offset] || s.offset < r.pos || s.offset >= len(data) {
					return true, errBIFF
				}
				seen[s.offset] = true
				child := &biffReader{data: data, pos: s.offset, records: &records}
				id, bof, err := child.next()
				if err != nil {
					return true, err
				}
				v, kind, err := biffBOF(id, bof)
				if err != nil || v != version || kind != 0x0010 {
					return true, errBIFF
				}
				rows, err := biffSheet(child, version, enc)
				if err == nil {
					err = add(s.name, rows)
				}
				if err != nil {
					return true, err
				}
			}
			return true, nil
		case 0x002f:
			return true, errors.New("Excel workbook is password-protected; remove the password and upload it again")
		case 0x0042:
			enc, err = biffCodepage(payload)
			if err != nil {
				return true, err
			}
		case 0x0085:
			if version == 4 {
				continue
			} // Actual BIFF4W locations/names are in SHEETHDR.
			if len(payload) < 7 {
				return true, errBIFF
			}
			if payload[5] != 0 {
				continue
			} // Charts, macros and modules are not worksheets.
			name, err := biffText(nil, payload, 6, 1, enc)
			if err != nil {
				return true, err
			}
			sheets = append(sheets, sheet{name, int(binary.LittleEndian.Uint32(payload))})
			if len(sheets) > 1024 {
				return true, errors.New("Excel workbook has too many sheets")
			}
		case 0x008f: // BIFF4W embeds each sheet after its header, including its BOF.
			if version != 4 || len(payload) < 5 {
				return true, errBIFF
			}
			length := int(binary.LittleEndian.Uint32(payload))
			if length < 8 || length > len(data)-r.pos {
				return true, errBIFF
			}
			name, err := biffText(nil, payload, 4, 1, enc)
			if err != nil {
				return true, err
			}
			child := &biffReader{data: data[r.pos : r.pos+length], records: &records}
			id, bof, err := child.next()
			if err != nil {
				return true, err
			}
			v, kind, err := biffBOF(id, bof)
			if err != nil || v != 4 || kind != 0x0010 {
				return true, errBIFF
			}
			rows, err := biffSheet(child, v, enc)
			if err == nil {
				err = add(name, rows)
			}
			if err != nil {
				return true, err
			}
			r.pos += length
		}
	}
}

func biffNumber(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", errBIFF
	}
	return strconv.FormatFloat(value, 'g', -1, 64), nil
}
func biffRK(data []byte) (string, error) {
	if len(data) < 4 {
		return "", errBIFF
	}
	u := binary.LittleEndian.Uint32(data)
	value := math.Float64frombits(uint64(u&^3) << 32)
	if u&2 != 0 {
		value = float64(int32(u) >> 2)
	}
	if u&1 != 0 {
		value /= 100
	}
	return biffNumber(value)
}
func biffBoolError(value, isError byte) (string, error) {
	if isError == 0 {
		if value == 0 {
			return "FALSE", nil
		}
		if value == 1 {
			return "TRUE", nil
		}
		return "", errBIFF
	}
	errors := map[byte]string{0: "#NULL!", 7: "#DIV/0!", 15: "#VALUE!", 23: "#REF!", 29: "#NAME?", 36: "#NUM!", 42: "#N/A"}
	if message := errors[value]; isError == 1 && message != "" {
		return message, nil
	}
	return "", errBIFF
}

func biffSheet(r *biffReader, version int, enc encoding.Encoding) ([][]string, error) {
	cells := map[int]map[int]string{}
	columns, size, count := 0, 0, 0
	put := func(row, col int, value string) error {
		count++
		size += len(value) + 3
		if col > 255 || row > 65535 || size > maxExtractedText || count > 100000 {
			return errors.New("Excel worksheet exceeds the text or cell limit")
		}
		if cells[row] == nil {
			cells[row] = map[int]string{}
		}
		cells[row][col] = value
		columns = max(columns, col+1)
		return nil
	}
	pendingRow, pendingCol := -1, -1
	for {
		id, data, err := r.next()
		if err != nil {
			return nil, err
		}
		if id == 0x000a {
			if pendingRow >= 0 {
				return nil, errBIFF
			}
			indexes := make([]int, 0, len(cells))
			for index := range cells {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			if len(indexes)*columns > 1000000 {
				return nil, errors.New("Excel worksheet exceeds the table cell limit")
			}
			rows := [][]string{}
			for _, index := range indexes {
				row := make([]string, columns)
				nonempty := false
				for col, value := range cells[index] {
					row[col] = value
					nonempty = nonempty || value != ""
				}
				if nonempty {
					rows = append(rows, row)
				}
			}
			return rows, nil
		}
		switch id {
		case 0x0009, 0x0209, 0x0409, 0x0809:
			// Embedded chart substreams have their own EOF. Do not end the
			// worksheet early or confuse chart records with worksheet cells.
			for depth := 1; depth > 0; {
				id, _, err := r.next()
				if err != nil {
					return nil, err
				}
				if id == 0x000a {
					depth--
				}
				if id == 0x0009 || id == 0x0209 || id == 0x0409 || id == 0x0809 {
					depth++
				}
				if depth > 32 {
					return nil, errBIFF
				}
			}
			continue
		case 0x002f:
			return nil, errors.New("Excel workbook is password-protected; remove the password and upload it again")
		case 0x0042:
			enc, err = biffCodepage(data)
			if err != nil {
				return nil, err
			}
			continue
		case 0x0007, 0x0207:
			if pendingRow < 0 {
				return nil, errBIFF
			}
			width := 2
			if version == 2 {
				width = 1
			}
			value, err := biffText(r, data, 0, width, enc)
			if err == nil {
				err = put(pendingRow, pendingCol, value)
			}
			if err != nil {
				return nil, err
			}
			pendingRow, pendingCol = -1, -1
			continue
		}
		switch id {
		case 0x0002, 0x0003, 0x0004, 0x0005, 0x0203, 0x0204, 0x0205, 0x00d6, 0x027e, 0x00bd, 0x0006, 0x0206, 0x0406:
		default:
			continue // Formatting, metadata and formula programs are not cell values.
		}
		if len(data) < 6 || pendingRow >= 0 {
			return nil, errBIFF
		}
		row, col := int(binary.LittleEndian.Uint16(data)), int(binary.LittleEndian.Uint16(data[2:]))
		offset := 6
		if version == 2 {
			offset = 7
		}
		if len(data) < offset {
			return nil, errBIFF
		}
		value := ""
		switch id {
		case 0x0002:
			if len(data)-offset < 2 {
				return nil, errBIFF
			}
			value = strconv.Itoa(int(binary.LittleEndian.Uint16(data[offset:])))
		case 0x0003, 0x0203:
			if len(data)-offset < 8 {
				return nil, errBIFF
			}
			value, err = biffNumber(math.Float64frombits(binary.LittleEndian.Uint64(data[offset:])))
		case 0x0004, 0x0204, 0x00d6:
			width := 2
			if version == 2 {
				width = 1
			}
			value, err = biffText(r, data, offset, width, enc)
		case 0x0005, 0x0205:
			if len(data)-offset < 2 {
				return nil, errBIFF
			}
			value, err = biffBoolError(data[offset], data[offset+1])
		case 0x027e:
			value, err = biffRK(data[offset:])
		case 0x00bd:
			last := int(binary.LittleEndian.Uint16(data[len(data)-2:]))
			if last < col || last > 255 || len(data) != 6+(last-col+1)*6 {
				return nil, errBIFF
			}
			for c := col; c <= last; c++ {
				value, err = biffRK(data[6+(c-col)*6:])
				if err == nil {
					err = put(row, c, value)
				}
				if err != nil {
					return nil, err
				}
			}
			continue
		case 0x0006, 0x0206, 0x0406:
			if len(data)-offset < 8 {
				return nil, errBIFF
			}
			cached := data[offset : offset+8]
			if cached[6] != 255 || cached[7] != 255 {
				value, err = biffNumber(math.Float64frombits(binary.LittleEndian.Uint64(cached)))
			} else {
				switch cached[0] {
				case 0:
					pendingRow, pendingCol = row, col
					continue
				case 1:
					value, err = biffBoolError(cached[2], 0)
				case 2:
					value, err = biffBoolError(cached[2], 1)
				case 3:
					value = ""
				default:
					return nil, errBIFF
				}
			}
		}
		if err == nil {
			err = put(row, col, value)
		}
		if err != nil {
			return nil, err
		}
	}
}
