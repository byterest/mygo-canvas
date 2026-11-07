//go:build ios

package ios

import (
	"math"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/transfer"
)

// surface is the view of a window, a UIView a GPU renderer adds its
// CAMetalLayer to (internal/gpu/metal). A display link paces its frames.
//
// Touches become the pointer: one finger presses where it went down once
// it moves, or taps where it lifts, so that a second finger landing first
// makes the two a gesture instead, which scrolls by how their middle moves
// and zooms by how far apart they go, as a touchpad's do. The Apple Pencil
// presses at once: it draws.
type surface struct {
	w    *window
	view id
	link id

	running bool // the display link ticks
	due     bool // a frame is asked for
	idle    int  // ticks with no frame asked for
	w0, h0  float64

	touches map[id]*touch
	primary id   // the touch that is the pointer
	pressed bool // the pointer is down
	gesture bool // two fingers scroll and zoom
	cx, cy  float64
	dist    float64

	input platform.TextInputState
}

type touch struct {
	x0, y0, x, y float64
	pen          bool
}

// moveSlop is how far a finger moves, in points, before it presses.
const moveSlop = 6

func newSurface(w *window, r cgRect) *surface {
	s := &surface{w: w, touches: map[id]*touch{}}
	s.view = msgInitRect(send(class("MyGoSurfaceView"), "alloc"), sel("initWithFrame:"), r)
	send(s.view, "setMultipleTouchEnabled:", true)
	send(s.view, "setOpaque:", false)
	s.link = send(class("CADisplayLink"), "displayLinkWithTarget:selector:", s.view, sel("mygoTick:"))
	send(s.link, "retain")
	send(s.link, "setPaused:", true)
	send(s.link, "addToRunLoop:forMode:", send(class("NSRunLoop"), "mainRunLoop"), kCFRunLoopCommonModes)
	w.b.bySurface[s.view] = s
	return s
}

func (s *surface) destroy() {
	if s.link != 0 {
		send(s.link, "invalidate")
		send(s.link, "release")
		s.link = 0
	}
	delete(s.w.b.bySurface, s.view)
}

func (s *surface) send(ev platform.SurfaceEvent) bool {
	if s.w.closed {
		return false
	}
	return s.w.h.SurfaceEvent(ev)
}

func (s *surface) Native() platform.SurfaceNative {
	return platform.SurfaceNative{View: uintptr(s.view), Layer: uintptr(send(s.view, "layer"))}
}

func (s *surface) scale() float64 {
	if f := msgFloat(s.view, sel("contentScaleFactor")); f > 0 {
		return f
	}
	_, scale := mainScreen()
	return scale
}

func (s *surface) Size() (float64, float64, float64) {
	b := msgRect(s.view, sel("bounds"))
	return b.Size.W, b.Size.H, s.scale()
}

// RequestFrame has the display link draw a frame at its next tick.
func (s *surface) RequestFrame() {
	if s.w.closed {
		return
	}
	s.due = true
	if !s.running {
		s.running = true
		send(s.link, "setPaused:", false)
	}
}

// tick draws the frame asked for, and pauses the link once a few ticks
// came with none.
func (s *surface) tick() {
	if !s.due {
		if s.idle++; s.idle > 4 {
			s.running = false
			send(s.link, "setPaused:", true)
		}
		return
	}
	s.due, s.idle = false, 0
	s.send(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
}

func (s *surface) RefreshRate() float64 {
	return float64(sendInt(send(class("UIScreen"), "mainScreen"), "maximumFramesPerSecond"))
}

// PresentPixels has nothing to show frames drawn in memory with: a
// device and the simulator both have Metal.
func (s *surface) PresentPixels(pix []byte, stride, width, height int) {}

func (s *surface) SetCursor(platform.Cursor) {}

func (s *surface) SetTextInput(t platform.TextInputState) { s.input = t }

func (s *surface) UpdateAccessibility(*platform.AccessTree)  {}
func (s *surface) StartDataDrag(platform.DragRequest)        {}
func (s *surface) CancelDataDrag()                           {}
func (s *surface) SetDropFormats([]transfer.Format)          {}
func (s *surface) PlaceWebViews([]platform.WebViewPlacement) {}
func (s *surface) NewWebView(*platform.WindowOptions, platform.WindowHandler) (platform.WebView, error) {
	return nil, platform.ErrUnsupported
}

// layout tells the content of a new size, and draws it at once, with the
// change: a rotation shows the frame of the new size.
func (s *surface) layout() {
	w, h, _ := s.Size()
	if w == s.w0 && h == s.h0 {
		return
	}
	s.w0, s.h0 = w, h
	s.send(platform.SurfaceEvent{Kind: platform.SurfaceResize})
	s.due = false
	s.send(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
}

// --- touches ---

// eachTouch calls fn with the touches of a set.
func eachTouch(set id, fn func(t id)) {
	all := send(set, "allObjects")
	for i := range sendInt(all, "count") {
		fn(send(all, "objectAtIndex:", uintptr(i)))
	}
}

func (s *surface) where(t id) (float64, float64) {
	p := msgPointIn(t, sel("locationInView:"), s.view)
	return p.X, p.Y
}

func (s *surface) pointer(kind platform.SurfaceEventKind, x, y float64, clicks int) {
	s.send(platform.SurfaceEvent{Kind: kind, X: x, Y: y, Clicks: clicks})
}

func (s *surface) touchesBegan(set id) {
	eachTouch(set, func(t id) {
		x, y := s.where(t)
		s.touches[t] = &touch{x0: x, y0: y, x: x, y: y, pen: sendInt(t, "type") == 2}
	})
	if s.gesture {
		return
	}
	if len(s.touches) >= 2 {
		// A second finger: the two are a gesture, and the first presses
		// nothing, or lets go of what it pressed.
		if p := s.touches[s.primary]; p != nil && s.pressed && !p.pen {
			s.pointer(platform.PointerUp, p.x, p.y, 0)
		}
		if p := s.touches[s.primary]; p != nil && p.pen {
			return // the pen keeps drawing; a palm or a finger beside it is ignored
		}
		s.primary, s.pressed, s.gesture = 0, false, true
		s.cx, s.cy, s.dist = s.pair()
		return
	}
	for t, tc := range s.touches {
		s.primary = t
		if tc.pen {
			s.pointer(platform.PointerMove, tc.x, tc.y, 0)
			s.pointer(platform.PointerDown, tc.x, tc.y, sendInt(t, "tapCount"))
			s.pressed = true
		}
	}
}

// pair returns the middle of the first two touches and how far apart they
// are.
func (s *surface) pair() (cx, cy, dist float64) {
	var pts [][2]float64
	for _, t := range s.touches {
		pts = append(pts, [2]float64{t.x, t.y})
		if len(pts) == 2 {
			break
		}
	}
	if len(pts) < 2 {
		return 0, 0, 0
	}
	a, b := pts[0], pts[1]
	return (a[0] + b[0]) / 2, (a[1] + b[1]) / 2, math.Hypot(a[0]-b[0], a[1]-b[1])
}

func (s *surface) touchesMoved(set id) {
	eachTouch(set, func(t id) {
		if tc := s.touches[t]; tc != nil {
			tc.x, tc.y = s.where(t)
		}
	})
	if s.gesture {
		if len(s.touches) < 2 {
			return
		}
		cx, cy, dist := s.pair()
		if dx, dy := cx-s.cx, cy-s.cy; dx != 0 || dy != 0 {
			s.send(platform.SurfaceEvent{Kind: platform.PointerScroll, X: cx, Y: cy, DX: -dx, DY: -dy, Precise: true})
		}
		if s.dist > 0 && dist > 0 {
			if k := dist / s.dist; math.Abs(k-1) > 1e-3 {
				s.send(platform.SurfaceEvent{Kind: platform.PointerZoom, X: cx, Y: cy, Scale: k})
			}
		}
		s.cx, s.cy, s.dist = cx, cy, dist
		return
	}
	tc := s.touches[s.primary]
	if tc == nil {
		return
	}
	if !s.pressed {
		if math.Hypot(tc.x-tc.x0, tc.y-tc.y0) < moveSlop {
			return
		}
		// It moved: it pressed where it went down, and drags from there.
		s.pointer(platform.PointerMove, tc.x0, tc.y0, 0)
		s.pointer(platform.PointerDown, tc.x0, tc.y0, 1)
		s.pressed = true
	}
	s.pointer(platform.PointerMove, tc.x, tc.y, 0)
}

func (s *surface) touchesEnded(set id, cancelled bool) {
	eachTouch(set, func(t id) {
		tc := s.touches[t]
		if tc == nil {
			return
		}
		x, y := s.where(t)
		delete(s.touches, t)
		if t != s.primary {
			return
		}
		switch {
		case s.pressed:
			s.pointer(platform.PointerUp, x, y, 0)
		case !cancelled:
			// A tap: a press and a release where it went down.
			clicks := max(sendInt(t, "tapCount"), 1)
			s.pointer(platform.PointerMove, tc.x0, tc.y0, 0)
			s.pointer(platform.PointerDown, tc.x0, tc.y0, clicks)
			s.pointer(platform.PointerUp, tc.x0, tc.y0, clicks)
		}
		s.primary, s.pressed = 0, false
	})
	if s.gesture && len(s.touches) == 0 {
		s.gesture = false
	}
	if len(s.touches) < 2 && s.gesture {
		s.dist = 0
	}
}

// --- the view's class ---

func registerSurfaceClass() {
	surfaceOf := func(self id) *surface { return theBackend.bySurface[self] }
	_, err := objc.RegisterClass("MyGoSurfaceView", objc.GetClass("UIView"), nil, nil, []objc.MethodDef{
		{Cmd: sel("mygoTick:"), Fn: func(self id, _ objc.SEL, link id) {
			if s := surfaceOf(self); s != nil {
				s.tick()
			}
		}},
		{Cmd: sel("layoutSubviews"), Fn: func(self id, _ objc.SEL) {
			self.SendSuper(sel("layoutSubviews"))
			if s := surfaceOf(self); s != nil {
				s.layout()
			}
		}},
		{Cmd: sel("canBecomeFirstResponder"), Fn: func(self id, _ objc.SEL) bool { return true }},
		{Cmd: sel("touchesBegan:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
			if s := surfaceOf(self); s != nil {
				s.touchesBegan(set)
			}
		}},
		{Cmd: sel("touchesMoved:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
			if s := surfaceOf(self); s != nil {
				s.touchesMoved(set)
			}
		}},
		{Cmd: sel("touchesEnded:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
			if s := surfaceOf(self); s != nil {
				s.touchesEnded(set, false)
			}
		}},
		{Cmd: sel("touchesCancelled:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
			if s := surfaceOf(self); s != nil {
				s.touchesEnded(set, true)
			}
		}},
		{Cmd: sel("pressesBegan:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
			s := surfaceOf(self)
			if s == nil || !s.presses(set, true) {
				self.SendSuper(sel("pressesBegan:withEvent:"), set, ev)
			}
		}},
		{Cmd: sel("pressesEnded:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
			s := surfaceOf(self)
			if s == nil || !s.presses(set, false) {
				self.SendSuper(sel("pressesEnded:withEvent:"), set, ev)
			}
		}},
	})
	if err != nil {
		panic(err)
	}
	_, err = objc.RegisterClass("MyGoViewController", objc.GetClass("UIViewController"), nil, nil, []objc.MethodDef{
		// The home indicator fades while the canvas is used, and comes
		// back at a touch of the bottom edge.
		{Cmd: sel("prefersHomeIndicatorAutoHidden"), Fn: func(self id, _ objc.SEL) bool { return true }},
	})
	if err != nil {
		panic(err)
	}
}
