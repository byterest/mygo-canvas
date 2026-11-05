package ui

import (
	"math"
	"runtime"
	"testing"
)

// TestOpticalScaleKeepsLines zooms a paragraph, its box and its text
// together: set at the zoom, OpticalScale keeps its lines, as many at every
// zoom as at none.
func TestOpticalScaleKeepsLines(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS has fonts whose spacing follows their size")
	}
	const prose = "The cost of a frame follows what a window shows, so the interesting number is not the size of a document but the number of blocks a viewport holds, and how many lines each of them has."
	lines := func(zoom float32, optical bool) float32 {
		tt := NewTester(func(c *Context) {
			b := Box(c).Width(300 * zoom).FontSize(14 * zoom).LineHeight(1.5)
			if optical {
				b.OpticalScale(zoom)
			}
			b.Children(func() { Text(c, prose) })
		}, 2000, 2000)
		r, ok := tt.Find(prose)
		if !ok {
			t.Fatal("no paragraph")
		}
		return float32(math.Round(float64(r.H / (14 * zoom * 1.5))))
	}
	want := lines(1, false)
	changed := false
	for _, z := range []float32{0.5, 0.75, 1.5, 2, 4} {
		if lines(z, false) != want {
			changed = true
		}
		if got := lines(z, true); got != want {
			t.Errorf("at a zoom of %v the paragraph has %v lines, %v unzoomed", z, got, want)
		}
	}
	if !changed {
		t.Log("the paragraph kept its lines without OpticalScale too")
	}
}
