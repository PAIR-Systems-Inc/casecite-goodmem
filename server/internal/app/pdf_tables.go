package app

import (
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

const pdfGridTolerance = 3.0 // PDF points; matches the original table finder's snap/join tolerance.
var errPDFLayout = errors.New("PDF layout requires GoodMem OCR")

type pdfPoint struct{ x, y float64 }
type pdfSegment struct{ a, b pdfPoint }
type pdfMatrix [6]float64

func (m pdfMatrix) point(x, y float64) pdfPoint {
	return pdfPoint{m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]}
}
func (m pdfMatrix) compose(n pdfMatrix) pdfMatrix {
	return pdfMatrix{m[0]*n[0] + m[2]*n[1], m[1]*n[0] + m[3]*n[1], m[0]*n[2] + m[2]*n[3], m[1]*n[2] + m[3]*n[3], m[0]*n[4] + m[2]*n[5] + m[4], m[1]*n[4] + m[3]*n[5] + m[5]}
}

// Read painted rules and rectangle edges, including transformed paths. Text
// itself stays native. Form XObjects require OCR because the underlying text
// reader does not walk their content; accepting the surrounding header would
// otherwise silently omit the form's body.
func pdfPaintedRules(page pdf.Page) (lines []pdfSegment, err error) {
	defer func() {
		if recover() != nil {
			lines = nil
			err = errPDFLayout
		}
	}()
	if page.V.Key("Contents").IsNull() {
		return nil, nil
	}
	matrix := pdfMatrix{1, 0, 0, 1, 0, 0}
	stack := []pdfMatrix{}
	path := []pdfSegment{}
	var current, origin pdfPoint
	hasCurrent := false
	operations := 0
	add := func(a, b pdfPoint) {
		if len(path) >= 4096 {
			panic(errPDFLayout)
		}
		for _, v := range []float64{a.x, a.y, b.x, b.y} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e7 {
				panic(errPDFLayout)
			}
		}
		path = append(path, pdfSegment{a, b})
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
	pdf.Interpret(page.V.Key("Contents"), func(stk *pdf.Stack, op string) {
		operations++
		if operations > 500000 || stk.Len() > 4096 {
			panic(errPDFLayout)
		}
		args := make([]pdf.Value, stk.Len())
		for i := len(args) - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		require := func(n int) {
			if len(args) != n {
				panic(errPDFLayout)
			}
		}
		switch op {
		case "q":
			if len(stack) >= 256 {
				panic(errPDFLayout)
			}
			stack = append(stack, matrix)
		case "Q":
			if len(stack) == 0 {
				panic(errPDFLayout)
			}
			matrix = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		case "cm":
			require(6)
			var next pdfMatrix
			for i := range next {
				next[i] = args[i].Float64()
			}
			matrix = matrix.compose(next)
		case "Tm":
			require(6)
			// Rotated/sheared text does not have reliable native glyph anchors.
			if math.Abs(args[1].Float64()) > 1e-8 || math.Abs(args[2].Float64()) > 1e-8 || math.Abs(matrix[1]) > 1e-8 || math.Abs(matrix[2]) > 1e-8 {
				panic(errPDFLayout)
			}
		case "m":
			require(2)
			current = matrix.point(args[0].Float64(), args[1].Float64())
			origin = current
			hasCurrent = true
		case "l":
			require(2)
			if !hasCurrent {
				panic(errPDFLayout)
			}
			next := matrix.point(args[0].Float64(), args[1].Float64())
			add(current, next)
			current = next
		case "h":
			if hasCurrent {
				add(current, origin)
				current = origin
			}
		case "re":
			require(4)
			x, y, w, h := args[0].Float64(), args[1].Float64(), args[2].Float64(), args[3].Float64()
			p := []pdfPoint{matrix.point(x, y), matrix.point(x+w, y), matrix.point(x+w, y+h), matrix.point(x, y+h)}
			for i := range p {
				add(p[i], p[(i+1)%4])
			}
			origin, current, hasCurrent = p[0], p[0], true
		case "c":
			require(6)
			current = matrix.point(args[4].Float64(), args[5].Float64())
		case "v", "y":
			require(4)
			current = matrix.point(args[2].Float64(), args[3].Float64())
		case "S", "s", "f", "F", "f*", "B", "B*", "b", "b*":
			if (op == "s" || op == "b" || op == "b*") && hasCurrent {
				add(current, origin)
			}
			lines = append(lines, path...)
			if len(lines) > 8000 {
				panic(errPDFLayout)
			}
			path = nil
			hasCurrent = false
		case "n":
			path = nil
			hasCurrent = false
		case "Do":
			require(1)
			if resources.Key("XObject").Key(args[0].Name()).Key("Subtype").Name() == "Form" {
				panic(errPDFLayout)
			}
		}
	})
	return lines, nil
}

type pdfRule struct{ axis, lo, hi float64 }

func pdfGridRules(lines []pdfSegment) (horizontal, vertical []pdfRule) {
	for _, line := range lines {
		if math.Abs(line.a.y-line.b.y) < .5 && math.Abs(line.a.x-line.b.x) >= pdfGridTolerance {
			horizontal = append(horizontal, pdfRule{(line.a.y + line.b.y) / 2, math.Min(line.a.x, line.b.x), math.Max(line.a.x, line.b.x)})
		} else if math.Abs(line.a.x-line.b.x) < .5 && math.Abs(line.a.y-line.b.y) >= pdfGridTolerance {
			vertical = append(vertical, pdfRule{(line.a.x + line.b.x) / 2, math.Min(line.a.y, line.b.y), math.Max(line.a.y, line.b.y)})
		}
	}
	return
}
func pdfRuleAxes(rules []pdfRule) []float64 {
	values := make([]float64, len(rules))
	for i, r := range rules {
		values[i] = r.axis
	}
	sort.Float64s(values)
	axes := []float64{}
	for i := 0; i < len(values); {
		sum, count := values[i], 1
		j := i + 1
		for j < len(values) && values[j]-sum/float64(count) <= pdfGridTolerance {
			sum += values[j]
			count++
			j++
		}
		axes = append(axes, sum/float64(count))
		i = j
	}
	return axes
}
func pdfNearestAxis(axes []float64, value float64) int {
	i := sort.SearchFloat64s(axes, value)
	if i == len(axes) || i > 0 && math.Abs(axes[i-1]-value) < math.Abs(axes[i]-value) {
		i--
	}
	return i
}
func pdfMergedRules(rules []pdfRule, axes []float64) [][]pdfRule {
	out := make([][]pdfRule, len(axes))
	for _, r := range rules {
		i := pdfNearestAxis(axes, r.axis)
		out[i] = append(out[i], r)
	}
	for i, spans := range out {
		sort.Slice(spans, func(a, b int) bool { return spans[a].lo < spans[b].lo })
		merged := []pdfRule{}
		for _, span := range spans {
			n := len(merged)
			if n > 0 && span.lo <= merged[n-1].hi+pdfGridTolerance {
				merged[n-1].hi = math.Max(merged[n-1].hi, span.hi)
			} else {
				merged = append(merged, span)
			}
		}
		out[i] = merged
	}
	return out
}
func pdfRulesCover(rules []pdfRule, a, b float64) bool {
	lo, hi := math.Min(a, b), math.Max(a, b)
	i := sort.Search(len(rules), func(i int) bool { return rules[i].lo > lo+pdfGridTolerance }) - 1
	return i >= 0 && rules[i].hi >= hi-pdfGridTolerance
}

type pdfGridCell struct {
	left, right, bottom, top int
	group                    int
	glyphs                   []pdf.Text
}

func pdfGridCells(horizontal, vertical []pdfRule) (cells []pdfGridCell, xs, ys []float64, err error) {
	xs, ys = pdfRuleAxes(vertical), pdfRuleAxes(horizontal)
	if len(xs) > 512 || len(ys) > 512 {
		return nil, nil, nil, errPDFLayout
	}
	if len(xs) < 2 || len(ys) < 2 {
		return nil, xs, ys, nil
	}
	h, v := pdfMergedRules(horizontal, ys), pdfMergedRules(vertical, xs)
	checks := 0
	for bottom := 0; bottom < len(ys)-1; bottom++ {
		for left := 0; left < len(xs)-1; left++ {
			if !pdfRulesCover(h[bottom], xs[left], xs[left]) || !pdfRulesCover(v[left], ys[bottom], ys[bottom]) {
				continue
			}
			found := false
			for top := bottom + 1; top < len(ys) && !found; top++ {
				if !pdfRulesCover(v[left], ys[bottom], ys[top]) {
					break
				}
				for right := left + 1; right < len(xs); right++ {
					checks++
					if checks > 1000000 {
						return nil, nil, nil, errPDFLayout
					}
					if !pdfRulesCover(h[bottom], xs[left], xs[right]) {
						break
					}
					if pdfRulesCover(h[top], xs[left], xs[right]) && pdfRulesCover(v[right], ys[bottom], ys[top]) {
						cells = append(cells, pdfGridCell{left: left, right: right, bottom: bottom, top: top})
						found = true
						break
					}
				}
			}
		}
	}
	parents := make([]int, len(cells))
	for i := range parents {
		parents[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	corners := map[[2]int]int{}
	for i, c := range cells {
		for _, p := range [][2]int{{c.left, c.bottom}, {c.left, c.top}, {c.right, c.bottom}, {c.right, c.top}} {
			if prev, ok := corners[p]; ok {
				parents[root(i)] = root(prev)
			} else {
				corners[p] = i
			}
		}
	}
	counts := map[int]int{}
	for i := range cells {
		cells[i].group = root(i)
		counts[cells[i].group]++
	}
	kept := []pdfGridCell{}
	for _, c := range cells {
		if counts[c.group] > 1 {
			kept = append(kept, c)
		}
	}
	return kept, xs, ys, nil
}

type pdfTextLine struct {
	x, y  float64
	text  string
	table bool
}

func pdfGlyphLines(glyphs []pdf.Text) []pdfTextLine {
	sort.SliceStable(glyphs, func(i, j int) bool { return glyphs[i].Y > glyphs[j].Y })
	out := []pdfTextLine{}
	for i := 0; i < len(glyphs); {
		j := i + 1
		for j < len(glyphs) && glyphs[i].Y-glyphs[j].Y <= 3 {
			j++
		}
		row := glyphs[i:j]
		sort.SliceStable(row, func(a, b int) bool { return row[a].X < row[b].X })
		var text strings.Builder
		end := row[0].X
		for _, g := range row {
			if text.Len() > 0 && g.X-end > math.Max(1.5, g.FontSize*.2) {
				text.WriteByte(' ')
			}
			text.WriteString(g.S)
			end = math.Max(end, g.X+g.W)
		}
		if content := strings.TrimSpace(text.String()); content != "" {
			out = append(out, pdfTextLine{x: row[0].X, y: glyphs[i].Y, text: content})
		}
		i = j
	}
	return out
}

// Native table text keeps typed numbers and punctuation exact. Unsupported or
// excessively complex layouts fall back to GoodMem OCR through the caller.
func nativePDFTableText(page pdf.Page, plain string) (text string, tables []string, err error) {
	defer func() {
		if recover() != nil {
			text = ""
			tables = nil
			err = errPDFLayout
		}
	}()
	rules, err := pdfPaintedRules(page)
	if err != nil {
		return "", nil, err
	}
	h, v := pdfGridRules(rules)
	cells, xs, ys, err := pdfGridCells(h, v)
	if err != nil {
		return "", nil, err
	}
	if len(cells) == 0 {
		return plain, nil, nil
	}
	slots := make([]int, (len(xs)-1)*(len(ys)-1))
	for i := range slots {
		slots[i] = -1
	}
	for i, c := range cells {
		for y := c.bottom; y < c.top; y++ {
			for x := c.left; x < c.right; x++ {
				slot := y*(len(xs)-1) + x
				if slots[slot] >= 0 {
					return "", nil, errPDFLayout
				}
				slots[slot] = i
			}
		}
	}
	outside := []pdf.Text{}
	glyphs := page.Content().Text
	if len(glyphs) > 1000000 {
		return "", nil, errPDFLayout
	}
	for _, g := range glyphs {
		if strings.TrimSpace(g.S) != "" && (g.FontSize <= 0 || math.IsNaN(g.X) || math.IsNaN(g.Y)) {
			return "", nil, errPDFLayout
		}
		cx, cy := g.X+math.Max(0, g.W)/2, g.Y+g.FontSize*.35
		x := sort.Search(len(xs), func(i int) bool { return xs[i] > cx }) - 1
		y := sort.Search(len(ys), func(i int) bool { return ys[i] > cy }) - 1
		cell := -1
		if x >= 0 && y >= 0 && x < len(xs)-1 && y < len(ys)-1 {
			cell = slots[y*(len(xs)-1)+x]
		}
		if cell >= 0 {
			cells[cell].glyphs = append(cells[cell].glyphs, g)
		} else {
			outside = append(outside, g)
		}
	}
	pieces := pdfGlyphLines(outside)
	groups := map[int][]pdfGridCell{}
	for _, c := range cells {
		groups[c.group] = append(groups[c.group], c)
	}
	tablePieces := []pdfTextLine{}
	for _, group := range groups {
		columns, rows := []int{}, []int{}
		seenX, seenY := map[int]bool{}, map[int]bool{}
		for _, c := range group {
			if !seenX[c.left] {
				columns = append(columns, c.left)
				seenX[c.left] = true
			}
			if !seenY[c.top] {
				rows = append(rows, c.top)
				seenY[c.top] = true
			}
		}
		sort.Ints(columns)
		sort.Sort(sort.Reverse(sort.IntSlice(rows)))
		matrix := make([][]string, len(rows))
		for i := range matrix {
			matrix[i] = make([]string, len(columns))
		}
		hasText := false
		for _, c := range group {
			lines := pdfGlyphLines(c.glyphs)
			parts := []string{}
			for _, line := range lines {
				parts = append(parts, line.text)
			}
			content := strings.Join(parts, "\n")
			hasText = hasText || content != ""
			row := sort.Search(len(rows), func(i int) bool { return rows[i] <= c.top })
			col := sort.SearchInts(columns, c.left)
			matrix[row][col] = content
		}
		if !hasText {
			continue
		}
		md, e := documentTable(matrix)
		if e != nil {
			return "", nil, fail(422, e.Error())
		}
		tablePieces = append(tablePieces, pdfTextLine{x: xs[columns[0]], y: ys[rows[0]], text: md, table: true})
	}
	// Preserve physical reading order, including text below a table. The original
	// reader moved all non-table text ahead of every table on the page.
	pieces = append(pieces, tablePieces...)
	sort.SliceStable(pieces, func(i, j int) bool {
		if pieces[i].y == pieces[j].y {
			return pieces[i].x < pieces[j].x
		}
		return pieces[i].y > pieces[j].y
	})
	var output strings.Builder
	previousTable := false
	for i, p := range pieces {
		if i > 0 {
			output.WriteByte('\n')
			if p.table || previousTable {
				output.WriteByte('\n')
			}
		}
		output.WriteString(p.text)
		previousTable = p.table
		if p.table {
			tables = append(tables, p.text)
		}
	}
	if output.Len() > maxExtractedText {
		return "", nil, fail(422, "Extracted text exceeds the 4 MB document limit")
	}
	return output.String(), tables, nil
}
