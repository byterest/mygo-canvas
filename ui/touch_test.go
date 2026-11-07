package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

func touchAt(tt *Tester, kind platform.SurfaceEventKind, x, y float32) {
	tt.send(platform.SurfaceEvent{Kind: kind, X: float64(x), Y: float64(y), Touch: true})
}

func TestTouchScrollsWhatScrolls(t *testing.T) {
	var s ScrollState
	clicked := 0
	tt := coreNewTester(func(c *context) {
		coreScroll(c).Fill().TrackScroll(&s).Children(func() {
			b := coreButton(c, "Row").Height(1000).Shrink(0)
			if b.Clicked() {
				clicked++
			}
		})
	}, 200, 200)
	touchAt(tt, platform.PointerMove, 100, 150)
	touchAt(tt, platform.PointerDown, 100, 150)
	touchAt(tt, platform.PointerMove, 100, 120)
	touchAt(tt, platform.PointerMove, 100, 90)
	if s.Y != 60 {
		t.Fatalf("a finger 60 up scrolled to %v, want 60", s.Y)
	}
	tt.Advance(200 * time.Millisecond) // it stops, then lets go: no glide
	touchAt(tt, platform.PointerUp, 100, 90)
	tt.Frame()
	if clicked != 0 {
		t.Errorf("a pan clicked what it started on")
	}
	if s.Y != 60 {
		t.Errorf("it glided on after stopping: %v", s.Y)
	}
}

func TestTouchGlides(t *testing.T) {
	var s ScrollState
	tt := coreNewTester(func(c *context) {
		coreScroll(c).Fill().TrackScroll(&s).Children(func() {
			coreBox(c).Height(5000).Shrink(0)
		})
	}, 200, 200)
	touchAt(tt, platform.PointerDown, 100, 180)
	for y := float32(170); y >= 100; y -= 10 {
		tt.Advance(10 * time.Millisecond)
		touchAt(tt, platform.PointerMove, 100, y)
	}
	touchAt(tt, platform.PointerUp, 100, 100)
	at := s.Y
	for range 20 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	if s.Y <= at+50 {
		t.Fatalf("let go fast at %v, it glided only to %v", at, s.Y)
	}
}

func TestTouchPressesWhatTakesMoves(t *testing.T) {
	var s ScrollState
	var off Point
	tt := coreNewTester(func(c *context) {
		coreScroll(c).Fill().TrackScroll(&s).Children(func() {
			coreBox(c).Width(50).Height(50).Draggable(&off, DraggableOptions{})
			coreBox(c).Height(1000).Shrink(0)
		})
	}, 200, 200)
	touchAt(tt, platform.PointerMove, 20, 20)
	touchAt(tt, platform.PointerDown, 20, 20)
	touchAt(tt, platform.PointerMove, 20, 60)
	touchAt(tt, platform.PointerMove, 20, 80)
	tt.Frame()
	if s.Y != 0 {
		t.Errorf("a finger on what drags scrolled to %v", s.Y)
	}
}
