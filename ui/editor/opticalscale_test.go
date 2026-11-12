package editor

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestOpticalScaleKeepsLineHeights zooms an editor's font with OpticalScale:
// its lines are those of the unzoomed font, snapped to whole pixels there,
// scaled, rather than snapped at each zoom, which made them a little taller
// or shorter from one zoom to the next.
func TestOpticalScaleKeepsLineHeights(t *testing.T) {
	lineHeight := func(zoom float32) float32 {
		st := &State{Buffer: NewBuffer("a\nb\nc\nd\n")}
		st.Font = ui.Font{Size: 13 * zoom, OpticalScale: zoom}
		tt := ui.NewTester(func(c *ui.Context) { View(c, st).Fill() }, 600, 400)
		tt.Frame()
		return st.ed.view.lineHeight / zoom
	}
	want := lineHeight(1)
	for _, z := range []float32{0.5, 0.7, 1.1, 1.3, 2.5} {
		if got := lineHeight(z); math.Abs(float64(got-want)) > 1e-3 {
			t.Errorf("zoomed %v, a line is %v tall unzoomed, want %v", z, got, want)
		}
	}
}

func TestOpticalScaleKeepsWrapsWithSharpGlyphFonts(t *testing.T) {
	for _, fit := range []bool{false, true} {
		for _, numbers := range []LineNumbers{LineNumbersOff, LineNumbersOn} {
			var want [][]int
			var baseTextX, baseWidth, baseLine float32
			for _, zoom := range []float32{1, .4, .55, .73, .9, 1.1, 1.3, 1.75, 2.5} {
				st := &State{Buffer: NewBuffer(""), FitContent: fit, SoftWrap: true, NoFolding: true, LineNumbers: numbers,
					Font: ui.Font{Size: 13 * zoom, OpticalScale: zoom}}
				tt := ui.NewTester(func(c *ui.Context) { View(c, st).Width(286 * zoom).Height(600 * zoom) }, 1000, 1600)
				tt.Frame()
				v := &st.ed.view
				texts := []string{strings.Repeat("a long code line with words ", 8), "    " + strings.Repeat("abcdefghi", 30), strings.Repeat("中文 test ", 30)}
				var got [][]int
				for _, text := range texts {
					got = append(got, v.wrapBreaks(text, nil))
				}
				if zoom == 1 {
					want = got
					baseTextX, baseWidth, baseLine = st.ed.textX, v.wrapWidth, v.lineHeight
				}
				for i := range got {
					if !slices.Equal(got[i], want[i]) {
						t.Errorf("fit=%v numbers=%v zoom=%v text=%d: breaks=%v want=%v", fit, numbers, zoom, i, got[i], want[i])
					}
				}
				if math.Abs(float64(st.ed.textX-baseTextX*zoom)) > .001 || math.Abs(float64(v.wrapWidth-baseWidth*zoom)) > .001 {
					t.Errorf("zoom=%v did not scale editor chrome and wrap width proportionally", zoom)
				}
				if v.font.Size != 13*zoom || math.Abs(float64(v.lineHeight-baseLine*zoom)) > .001 {
					t.Fatal("glyphs were not requested at their displayed size")
				}
			}
		}
	}
}
