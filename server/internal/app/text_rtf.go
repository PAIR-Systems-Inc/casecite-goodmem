package app

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// A bounded RTF text reader. Groups scope destinations, character encoding and
// Unicode fallback lengths; field instructions and embedded binary objects do
// not leak into contract text. No converter process or remote service is used.
func rtfText(data []byte) (string, error) {
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte(`{\rtf`)) {
		return "", errors.New("Invalid RTF document")
	}
	type state struct {
		skip      bool
		fontTable bool
		font      int
		fallback  int
		encoding  encoding.Encoding
	}
	current := state{fallback: 1, encoding: charmap.Windows1252}
	defaultFont := 0
	fonts := map[int]encoding.Encoding{}
	stack := []state{}
	units := []uint16{}
	skipBytes := 0
	var encoded []byte
	flush := func() error {
		if len(encoded) == 0 {
			return nil
		}
		charset := current.encoding
		if fontEncoding, declared := fonts[current.font]; declared {
			if fontEncoding == nil {
				return errors.New("Unsupported RTF font character set; save as DOCX or UTF-8 text")
			}
			charset = fontEncoding
		}
		decoded, err := charset.NewDecoder().Bytes(encoded)
		encoded = nil
		if err == nil {
			units = append(units, utf16.Encode([]rune(string(decoded)))...)
		}
		return err
	}
	putByte := func(b byte) {
		if skipBytes > 0 {
			skipBytes--
			return
		}
		if !current.skip {
			encoded = append(encoded, b)
		}
	}
	put := func(text string) error {
		if err := flush(); err != nil {
			return err
		}
		if skipBytes > 0 {
			skipBytes--
			return nil
		}
		if !current.skip {
			units = append(units, utf16.Encode([]rune(text))...)
		}
		return nil
	}
	destinations := map[string]bool{"fonttbl": true, "colortbl": true, "stylesheet": true, "info": true, "pict": true, "object": true, "objdata": true, "filetbl": true, "listtable": true, "listoverridetable": true, "revtbl": true, "rsidtbl": true, "generator": true, "fldinst": true, "xmlopen": true, "xmlclose": true, "datastore": true, "themedata": true, "colorschememapping": true}
	for i := 0; i < len(data); {
		if len(units) > maxExtractedText || len(encoded) > maxExtractedText {
			return "", errors.New("RTF text exceeds the 4 MB document limit")
		}
		b := data[i]
		i++
		switch b {
		case '{':
			if err := flush(); err != nil {
				return "", err
			}
			if len(stack) >= 256 {
				return "", errors.New("RTF groups are nested too deeply")
			}
			stack = append(stack, current)
		case '}':
			if err := flush(); err != nil {
				return "", err
			}
			if len(stack) == 0 {
				return "", errors.New("Unbalanced RTF groups")
			}
			current = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			skipBytes = 0
		case '\r', '\n':
		case '\\':
			if i >= len(data) {
				return "", errors.New("Truncated RTF control")
			}
			c := data[i]
			i++
			switch c {
			case '\\', '{', '}':
				putByte(c)
				continue
			case '*':
				if err := flush(); err != nil {
					return "", err
				}
				current.skip = true
				continue
			case '~':
				if err := put("\u00a0"); err != nil {
					return "", err
				}
				continue
			case '_':
				if err := put("-"); err != nil {
					return "", err
				}
				continue
			case '-':
				if skipBytes > 0 {
					skipBytes--
				}
				continue
			case '\'':
				if i+2 > len(data) {
					return "", errors.New("Truncated RTF byte escape")
				}
				n, err := strconv.ParseUint(string(data[i:i+2]), 16, 8)
				if err != nil {
					return "", errors.New("Invalid RTF byte escape")
				}
				i += 2
				putByte(byte(n))
				continue
			}
			if c < 'a' || c > 'z' {
				continue
			}
			start := i - 1
			for i < len(data) && ((data[i] >= 'a' && data[i] <= 'z') || (data[i] >= 'A' && data[i] <= 'Z')) {
				i++
			}
			control := string(data[start:i])
			start = i
			if i < len(data) && data[i] == '-' {
				i++
			}
			for i < len(data) && data[i] >= '0' && data[i] <= '9' {
				i++
			}
			n, hasNumber := 0, i > start
			if hasNumber {
				var err error
				n, err = strconv.Atoi(string(data[start:i]))
				if err != nil {
					return "", errors.New("Invalid RTF control parameter")
				}
			}
			if i < len(data) && data[i] == ' ' {
				i++
			}
			if err := flush(); err != nil {
				return "", err
			}
			if destinations[control] {
				current.skip = true
				if control == "fonttbl" {
					current.fontTable = true
				}
				continue
			}
			switch control {
			case "deff":
				if !hasNumber || n < 0 || n > 65535 {
					return "", errors.New("Invalid default RTF font")
				}
				defaultFont, current.font = n, n
			case "f":
				if !hasNumber || n < 0 || n > 65535 {
					return "", errors.New("Invalid RTF font")
				}
				current.font = n
			case "plain":
				current.font = defaultFont
			case "fcharset":
				if current.fontTable {
					code, known := map[int]int{0: 1252, 1: 0, 2: 1252, 42: 1252, 77: 10000, 84: 10007, 128: 932, 129: 949, 134: 936, 136: 950, 161: 1253, 162: 1254, 163: 1258, 177: 1255, 178: 1256, 186: 1257, 204: 1251, 222: 874, 238: 1250, 254: 437, 255: 850}[n]
					if n == 1 {
						delete(fonts, current.font)
					} else if known {
						fonts[current.font] = documentCodepage(code)
					} else {
						fonts[current.font] = nil
					}
					if len(fonts) > 4096 {
						return "", errors.New("RTF has too many fonts")
					}
				}
			case "cpg":
				if current.fontTable {
					fonts[current.font] = documentCodepage(n)
				}
			case "ansi":
				current.encoding = charmap.Windows1252
			case "mac":
				current.encoding = charmap.Macintosh
			case "pc":
				current.encoding = charmap.CodePage437
			case "pca":
				current.encoding = charmap.CodePage850
			case "bin":
				if !hasNumber || n < 0 || n > len(data)-i {
					return "", errors.New("Truncated RTF binary payload")
				}
				i += n
			case "uc":
				if !hasNumber || n < 0 || n > 16 {
					return "", errors.New("Invalid RTF Unicode fallback count")
				}
				current.fallback = n
			case "u":
				if !hasNumber || n < -32768 || n > 65535 {
					return "", errors.New("Invalid RTF Unicode escape")
				}
				if !current.skip {
					units = append(units, uint16(n))
				}
				skipBytes = current.fallback
			case "ansicpg":
				if encoding := documentCodepage(n); encoding != nil {
					current.encoding = encoding
				} else {
					return "", errors.New("Unsupported RTF code page; save as DOCX or UTF-8 text")
				}
			case "par", "line", "row":
				if err := put("\n"); err != nil {
					return "", err
				}
			case "page", "sect":
				if err := put("\n\n"); err != nil {
					return "", err
				}
			case "tab", "cell":
				if err := put("\t"); err != nil {
					return "", err
				}
			case "emdash":
				if err := put("—"); err != nil {
					return "", err
				}
			case "endash":
				if err := put("–"); err != nil {
					return "", err
				}
			case "lquote":
				if err := put("‘"); err != nil {
					return "", err
				}
			case "rquote":
				if err := put("’"); err != nil {
					return "", err
				}
			case "ldblquote":
				if err := put("“"); err != nil {
					return "", err
				}
			case "rdblquote":
				if err := put("”"); err != nil {
					return "", err
				}
			case "bullet":
				if err := put("•"); err != nil {
					return "", err
				}
			}
		default:
			putByte(b)
		}
	}
	if len(stack) != 0 {
		return "", errors.New("Unbalanced RTF groups")
	}
	if err := flush(); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(utf16.Decode(units))), nil
}
