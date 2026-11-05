package markdown

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestZoomScalesTheView zooms a view with Theme.Zoom: its table's columns,
// measured before its cells are laid out, and the view's height, borders
// and all, are the unzoomed ones, scaled.
func TestZoomScalesTheView(t *testing.T) {
	const src = "Intro.\n\n```go\nfunc main() {}\n```\n\n| Blocks | Lines | Frame |\n| --- | --- | --- |\n| 10 | 120 | 0.4 ms |\n| 1,000 | 12,000 | 11.0 ms |\n\n1. one\n2. two\n\n---\n\n> quoted\n\nLast.\n"
	measure := func(zoom float32) (columns, height float32) {
		tt := ui.NewTester(func(c *ui.Context) {
			th := DefaultTheme(c.Theme())
			th.FontSize = 14 * zoom
			th.Zoom = zoom
			ui.Box(c).Width(360 * zoom).Children(func() { View(c, src, &Options{Theme: th}) })
		}, 3000, 3000)
		blocks, ok1 := tt.Find("Blocks")
		frame, ok2 := tt.Find("Frame")
		intro, ok3 := tt.Find("Intro.")
		last, ok4 := tt.Find("Last.")
		if !ok1 || !ok2 || !ok3 || !ok4 {
			t.Fatalf("missing text: %q", tt.Texts())
		}
		return (frame.X - blocks.X) / zoom, (last.Y - intro.Y) / zoom
	}
	wantC, wantH := measure(1)
	for _, z := range []float32{0.5, 0.7, 1.3, 2, 3} {
		c, h := measure(z)
		if math.Abs(float64(c-wantC)) > 0.5 || math.Abs(float64(h-wantH)) > 0.5 {
			t.Errorf("zoomed %v: columns %v and height %v, want %v and %v", z, c, h, wantC, wantH)
		}
	}
}
