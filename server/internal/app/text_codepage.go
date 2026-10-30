package app

import (
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	textunicode "golang.org/x/text/encoding/unicode"
)

// Shared by legacy Excel and RTF. Unknown code pages fail visibly rather than
// silently corrupting a name or quotation using the machine's locale.
func documentCodepage(code int) encoding.Encoding {
	return map[int]encoding.Encoding{
		1250: charmap.Windows1250, 1251: charmap.Windows1251, 1252: charmap.Windows1252,
		1253: charmap.Windows1253, 1254: charmap.Windows1254, 1255: charmap.Windows1255,
		1256: charmap.Windows1256, 1257: charmap.Windows1257, 1258: charmap.Windows1258,
		874: charmap.Windows874, 437: charmap.CodePage437, 850: charmap.CodePage850,
		852: charmap.CodePage852, 855: charmap.CodePage855, 858: charmap.CodePage858,
		860: charmap.CodePage860, 862: charmap.CodePage862, 863: charmap.CodePage863,
		865: charmap.CodePage865, 866: charmap.CodePage866,
		932: japanese.ShiftJIS, 936: simplifiedchinese.GBK, 949: korean.EUCKR, 950: traditionalchinese.Big5,
		1200: textunicode.UTF16(textunicode.LittleEndian, textunicode.IgnoreBOM), 65001: textunicode.UTF8,
		10000: charmap.Macintosh, 10007: charmap.MacintoshCyrillic,
		28591: charmap.ISO8859_1, 28592: charmap.ISO8859_2, 28593: charmap.ISO8859_3,
		28594: charmap.ISO8859_4, 28595: charmap.ISO8859_5, 28596: charmap.ISO8859_6,
		28597: charmap.ISO8859_7, 28598: charmap.ISO8859_8, 28599: charmap.ISO8859_9,
		28603: charmap.ISO8859_13, 28605: charmap.ISO8859_15,
		20866: charmap.KOI8R, 21866: charmap.KOI8U,
	}[code]
}
