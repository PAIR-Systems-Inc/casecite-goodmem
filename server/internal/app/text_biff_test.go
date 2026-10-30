package app

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"testing"
)

// Optional independently downloaded files, decoded by the pinned Python reader.
// The repository's normal oracle corpus is self-generated and always tested.
func TestLegacyExcelPublishedReferences(t *testing.T) {
	path := os.Getenv("LEGACY_EXCEL_REFERENCE_JSON")
	if path == "" {
		t.Skip("external reference corpus not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []extractionFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("empty reference corpus")
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			out, err := extractNativeDocument(fixture.Content, fixture.Name)
			if err != nil {
				t.Fatal(err)
			}
			want := fixture.Text
			if fixture.Improved != nil {
				want = *fixture.Improved
			}
			if out.Text != want {
				t.Fatalf("text differs\ngot: %q\nwant: %q", out.Text, want)
			}
		})
	}
}

func biffTestRecord(id uint16, data []byte) []byte {
	b := make([]byte, 4, 4+len(data))
	binary.LittleEndian.PutUint16(b, id)
	binary.LittleEndian.PutUint16(b[2:], uint16(len(data)))
	return append(b, data...)
}

func TestLegacyExcelRejectsInvalidRecordsAndLimits(t *testing.T) {
	bof := biffTestRecord(0x209, []byte{0, 3, 0x10, 0, 0, 0})
	eof := biffTestRecord(0xa, nil)
	cell := func(value []byte) []byte { return append(make([]byte, 6), value...) }
	nan := make([]byte, 8)
	binary.LittleEndian.PutUint64(nan, math.Float64bits(math.NaN()))
	wide := cell([]byte{1, 0, 'x'})
	binary.LittleEndian.PutUint16(wide[2:], 256)
	for _, tc := range []struct {
		name    string
		records []byte
	}{
		{"missing EOF", nil},
		{"truncated header", []byte{1, 2, 3}},
		{"truncated payload", []byte{3, 2, 255, 255}},
		{"short number", biffTestRecord(0x203, cell([]byte{1}))},
		{"nonfinite number", biffTestRecord(0x203, cell(nan))},
		{"out of range column", biffTestRecord(0x204, wide)},
		{"truncated label", biffTestRecord(0x204, cell([]byte{10, 0, 'x'}))},
		{"unknown encoding", biffTestRecord(0x42, []byte{255, 255})},
		{"protected", biffTestRecord(0x2f, []byte{1, 2})},
		{"invalid boolean", biffTestRecord(0x205, cell([]byte{2, 0}))},
		{"invalid error", biffTestRecord(0x205, cell([]byte{255, 1}))},
		{"missing formula string", biffTestRecord(6, cell([]byte{0, 0, 0, 0, 0, 0, 255, 255}))},
		{"unexpected formula string", biffTestRecord(0x207, []byte{1, 0, 'x'})},
		{"bad multi RK extent", biffTestRecord(0xbd, make([]byte, 8))},
		{"excessive embedded depth", bytes.Repeat(bof, 33)},
		{"excessive records", bytes.Repeat(biffTestRecord(0x1234, nil), 500001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := append(append([]byte{}, bof...), tc.records...)
			if tc.name != "missing EOF" {
				data = append(data, eof...)
			}
			if _, err := spreadsheetText(data, ".xls"); err == nil {
				t.Fatal("invalid workbook accepted")
			}
			if _, err := validateDocumentUpload(data, "legacy.xls"); err == nil {
				t.Fatal("invalid raw workbook accepted for upload")
			}
		})
	}
	for _, fixture := range extractionFixtures(t) {
		if fixture.Name != "biff5-raw.xls" {
			continue
		}
		data := append([]byte{}, fixture.Content...)
		for pos := 0; pos+4 < len(data); {
			id := binary.LittleEndian.Uint16(data[pos:])
			length := int(binary.LittleEndian.Uint16(data[pos+2:]))
			pos += 4
			if id == 0x85 {
				binary.LittleEndian.PutUint32(data[pos:], uint32(len(data)+1))
				break
			}
			pos += length
		}
		if _, err := spreadsheetText(data, ".xls"); err == nil {
			t.Fatal("out-of-file sheet offset accepted")
		}
	}
}

func FuzzLegacyExcelRecords(f *testing.F) {
	f.Add([]byte{9, 0, 4, 0, 2, 0, 16, 0, 10, 0, 0, 0})
	f.Add([]byte{9, 4, 6, 0, 0, 4, 0, 1, 0, 0, 143, 0, 5, 0, 8, 0, 0, 0, 255})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		// Direct entry points deliberately have no panic-recovery wrapper.
		// A malformed upload must return an error rather than crash the server.
		_, _ = legacySpreadsheet(data, func(string, [][]string) error { return nil })
		_, _ = validateDocumentUpload(data, "legacy.xls")
	})
}
