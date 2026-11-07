//go:build darwin && !ios

package darwin

import (
	"math"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

// A tablet's pen sends points of its own (NSEventTypeTabletPoint) as it
// moves, with whether its tip is down, and AppKit makes mouse events of
// some of them: a press only once the tip is pressed past the driver's
// threshold, a release while it still touches, and a drag for one point
// in five or so. A light hand then draws nothing, and the ends of what it
// draws are cut. So once the pen sends points, they are what presses,
// moves and releases the primary button, and the mouse events made of
// them are left out.

// pen is the state of a tablet's pen over a surface.
type pen struct {
	points bool // the pen sends points of its own
	down   bool // its tip is down, as a press of the primary button
	// The last press, for counting clicks as AppKit does.
	lastDown     time.Time
	lastX, lastY float64
	clicks       int
}

// NSEvent's subtype of a mouse event made from a tablet's point.
const nsEventSubtypeTabletPoint = 1

// penOwns reports whether a mouse event of the primary button is one the
// pen's own points stand for: a press, drag or release made from them.
// Moves of a pen held over the tablet are left as they are.
func (s *surface) penOwns(kind platform.SurfaceEventKind, ev id) bool {
	if !s.pen.points || int16(sendInt(ev, "subtype")) != nsEventSubtypeTabletPoint {
		return false
	}
	const mouseMoved = 5
	return kind != platform.PointerMove || sendInt(ev, "type") != mouseMoved || s.pen.down
}

// tabletPoint takes a point of the pen.
func (s *surface) tabletPoint(ev id) {
	s.pen.points = true
	tip := sendInt(ev, "buttonMask")&1 != 0
	x, y := s.location(ev)
	mods := eventMods(ev)
	switch {
	case tip && !s.pen.down:
		s.pen.down = true
		send(s.w.win, "makeFirstResponder:", uintptr(s.view))
		s.send(platform.SurfaceEvent{Kind: platform.PointerDown, X: x, Y: y, Clicks: s.penClicks(x, y), Mods: mods})
	case tip:
		s.send(platform.SurfaceEvent{Kind: platform.PointerMove, X: x, Y: y, Mods: mods})
	case s.pen.down:
		s.pen.down = false
		s.send(platform.SurfaceEvent{Kind: platform.PointerUp, X: x, Y: y, Clicks: s.pen.clicks, Mods: mods})
	}
}

// tabletProximity takes the pen coming to the tablet or leaving it: a pen
// taken away with its tip down lets go.
func (s *surface) tabletProximity(ev id) {
	if sendInt(ev, "isEnteringProximity")&1 != 0 {
		return
	}
	if s.pen.down {
		s.pen.down = false
		x, y := s.location(ev)
		s.send(platform.SurfaceEvent{Kind: platform.PointerUp, X: x, Y: y, Clicks: s.pen.clicks, Mods: eventMods(ev)})
	}
}

// penClicks counts a press of the pen as AppKit counts clicks: one more
// than the last within the system's double-click interval and a few DIPs
// of it, one otherwise.
func (s *surface) penClicks(x, y float64) int {
	now := time.Now()
	interval := time.Duration(msgFloat(class("NSEvent"), sel("doubleClickInterval")) * float64(time.Second))
	p := &s.pen
	if p.clicks > 0 && now.Sub(p.lastDown) <= interval &&
		math.Hypot(x-p.lastX, y-p.lastY) <= 4 {
		p.clicks++
	} else {
		p.clicks = 1
	}
	p.lastDown, p.lastX, p.lastY = now, x, y
	return p.clicks
}
