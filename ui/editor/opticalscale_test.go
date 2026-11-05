package editor

import (
	"math"
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
