package app

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	textunicode "golang.org/x/text/encoding/unicode"
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
		skip     bool
		fallback int
		encoding encoding.Encoding
	}
	current := state{fallback: 1, encoding: charmap.Windows1252}
	stack := []state{}
	units := []uint16{}
	skipBytes := 0
	var encoded []byte
	flush := func() error {
		if len(encoded) == 0 {
			return nil
		}
		decoded, err := current.encoding.NewDecoder().Bytes(encoded)
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
				continue
			}
			switch control {
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
				encodings := map[int]encoding.Encoding{1250: charmap.Windows1250, 1251: charmap.Windows1251, 1252: charmap.Windows1252, 1253: charmap.Windows1253, 1254: charmap.Windows1254, 1255: charmap.Windows1255, 1256: charmap.Windows1256, 1257: charmap.Windows1257, 1258: charmap.Windows1258, 437: charmap.CodePage437, 850: charmap.CodePage850, 932: japanese.ShiftJIS, 936: simplifiedchinese.GBK, 949: korean.EUCKR, 950: traditionalchinese.Big5, 65001: textunicode.UTF8, 10000: charmap.Macintosh, 28591: charmap.ISO8859_1, 20866: charmap.KOI8R}
				if encoding := encodings[n]; encoding != nil {
					current.encoding = encoding
				} else {
					return "", errors.New("Unsupported RTF code page; save as DOCX or UTF-8 text")
				}
			case "par", "line", "row":
				if err := put("\n"); err != nil {
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
