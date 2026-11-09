package editor

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// An editor that fits its text, in a scroll, types Space as a space rather
// than taking the scroll a page down.
func TestSpaceInAnEditorInAScrollTypes(t *testing.T) {
	st := &State{Buffer: NewBuffer(strings.Repeat("a line\n", 80))}
	st.FitContent = true
	var track ui.ScrollState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().TrackScroll(&track).Children(func() {
			View(c, st).AutoFocus()
		})
	}, 400, 300)
	tt.Frame()
	st.Select(0, 0)
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if track.Y != 0 {
		t.Errorf("Space scrolled the scroll to %v", track.Y)
	}
}
