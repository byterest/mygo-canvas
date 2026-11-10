package markdown

import (
	"image"
	"math"
	"testing"
	"time"

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

// TestZoomScalesPictures zooms a view with a picture narrower than it: the
// picture is as wide, against the text, at every zoom.
func TestZoomScalesPictures(t *testing.T) {
	pic := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 120, 60)))
	const src = "Intro.\n\n![Dot](dot.png)\n\nLast.\n"
	measure := func(zoom float32) (width, height float32) {
		o := &Options{LoadImage: func(string) (ui.ImageSource, error) { return pic, nil }}
		var tt *ui.Tester
		tt = ui.NewTester(func(c *ui.Context) {
			th := DefaultTheme(c.Theme())
			th.FontSize = 14 * zoom
			th.Zoom = zoom
			o.Theme = th
			ui.Box(c).Width(400 * zoom).Children(func() { View(c, src, o) })
		}, 3000, 3000)
		for range 20 {
			if _, ok := tt.Find("Dot"); ok {
				break
			}
			time.Sleep(5 * time.Millisecond)
			tt.Frame()
		}
		dot, ok1 := tt.Find("Dot")
		intro, ok2 := tt.Find("Intro.")
		last, ok3 := tt.Find("Last.")
		if !ok1 || !ok2 || !ok3 {
			t.Fatalf("missing: %q", tt.Texts())
		}
		return dot.W / zoom, (last.Y - intro.Y) / zoom
	}
	wantW, wantH := measure(1)
	if math.Abs(float64(wantW-120)) > 0.5 {
		t.Fatalf("at 1:1 the picture is %v wide, want 120", wantW)
	}
	for _, z := range []float32{0.5, 2, 3} {
		w, h := measure(z)
		if math.Abs(float64(w-wantW)) > 0.5 || math.Abs(float64(h-wantH)) > 0.5 {
			t.Errorf("zoomed %v: picture %v wide, height %v; want %v and %v", z, w, h, wantW, wantH)
		}
	}
}

// TestTableFill fills the view with a table of short cells, its columns
// sharing the room as they share the text; a table wider than the view
// still scrolls.
func TestTableFill(t *testing.T) {
	const src = "| A | Longer head |\n| --- | --- |\n| 1 | 2 |\n"
	tt := ui.NewTester(func(c *ui.Context) {
		th := DefaultTheme(c.Theme())
		th.TableFill = true
		ui.Box(c).Width(500).Children(func() { View(c, src, &Options{Theme: th}) })
	}, 800, 600)
	a, ok1 := tt.Find("A")
	b, ok2 := tt.Find("Longer head")
	if !ok1 || !ok2 {
		t.Fatalf("missing: %q", tt.Texts())
	}
	// The second column starts where the first ends: past half the view,
	// as wide as the table is, its share of 500 by its text.
	if b.X < 60 || b.X > 250 {
		t.Errorf("the second column starts at %v", b.X)
	}
	img := tt.Image()
	s := img.Bounds().Dx() / 800
	// The table's right border is near the view's right edge.
	found := false
	for x := 480; x < 500; x++ {
		if c := img.RGBAAt(x*s, int(a.Y+a.H/2)*s); c.R < 240 {
			found = true
		}
	}
	if !found {
		t.Errorf("no border near the view's right edge: the table does not fill it")
	}
}
