//go:build darwin && !ios

package darwin

import "testing"

func TestDiagonalResizeImageCursors(t *testing.T) {
	load()
	nwse := diagonalResizeCursor(false)
	nesw := diagonalResizeCursor(true)
	if nwse == 0 || nesw == 0 || nwse == nesw {
		t.Fatal("expected a separate image cursor for each diagonal direction")
	}
	for _, cursor := range []id{nwse, nesw} {
		if got := msgSize(send(cursor, "image"), sel("size")); got != (NSSize{Width: 18, Height: 18}) {
			t.Errorf("cursor size: %v", got)
		}
		if got := msgPoint(cursor, sel("hotSpot")); got != (NSPoint{X: 9, Y: 9}) {
			t.Errorf("cursor hot spot: %v", got)
		}
	}
	if diagonalResizeCursor(false) != nwse || diagonalResizeCursor(true) != nesw {
		t.Fatal("image cursors were not cached")
	}
}
