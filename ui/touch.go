package ui

import (
	"math"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

// touchPan is a finger moving content that scrolls, as on a phone: a
// press of a finger that moved at once (SurfaceEvent.Touch) over content
// that scrolls, with nothing under it taking the pointer's moves of its
// own, scrolls it rather than pressing it, as it does what drags a value
// (Drag), which a finger held still drags; let go moving, it glides on
// and slows down.
type touchPan struct {
	on     bool
	x, y   float32 // where it pans: what scrolls is under it
	lastX  float32
	lastY  float32
	lastAt time.Time
	vx, vy float32 // how fast the finger went, in DIPs a second

	gliding bool
	glideAt time.Time
}

// touchGlideFriction is how fast a glide slows: the speed falls by e every
// 1/touchGlideFriction seconds.
const touchGlideFriction = 3.2

// touchEvent takes the pointer's events while a finger pans, and a
// finger's press that starts one. It reports whether it took the event.
func (rt *engine) touchEvent(ev platform.SurfaceEvent, x, y float32) bool {
	p := &rt.touch
	switch ev.Kind {
	case platform.PointerDown:
		p.gliding = false // a finger catches what glides
		if !ev.Touch || ev.Button != 0 || !rt.touchScrolls(x, y) {
			return false
		}
		*p = touchPan{on: true, x: x, y: y, lastX: x, lastY: y, lastAt: rt.now()}
		rt.pointerX, rt.pointerY = x, y
		return true
	case platform.PointerMove:
		if !p.on {
			return false
		}
		now := rt.now()
		dx, dy := x-p.lastX, y-p.lastY
		if dt := float32(now.Sub(p.lastAt).Seconds()); dt > 0 {
			// The speed, smoothed over the last few moves.
			k := min(dt/0.05, 1)
			p.vx += (dx/dt - p.vx) * k
			p.vy += (dy/dt - p.vy) * k
		}
		p.lastX, p.lastY, p.lastAt = x, y, now
		rt.panBy(dx, dy)
		return true
	case platform.PointerUp:
		if !p.on {
			return false
		}
		p.on = false
		if rt.now().Sub(p.lastAt) > 80*time.Millisecond {
			p.vx, p.vy = 0, 0 // it stopped before it let go
		}
		if math.Hypot(float64(p.vx), float64(p.vy)) > 60 && !rt.preferences().ReduceMotion {
			p.gliding, p.glideAt = true, rt.now()
			rt.requestFrame()
		}
		return true
	}
	return false
}

// panBy scrolls what is under the pan by the finger's move: the content
// goes with the finger, so a finger going up scrolls down.
func (rt *engine) panBy(dx, dy float32) {
	px, py := rt.pointerX, rt.pointerY
	rt.pointerX, rt.pointerY = rt.touch.x, rt.touch.y
	rt.scroll(-dx, -dy, 0, true, false)
	rt.pointerX, rt.pointerY = px, py
}

// touchGlide moves what a finger let go of on, slower each frame, and
// reports whether it still glides.
func (rt *engine) touchGlide() {
	p := &rt.touch
	if !p.gliding {
		return
	}
	now := rt.now()
	dt := float32(now.Sub(p.glideAt).Seconds())
	if dt <= 0 {
		rt.requestFrame() // again in this instant: on in the next
		return
	}
	p.glideAt = now
	dt = min(dt, 0.05)
	rt.panBy(p.vx*dt, p.vy*dt)
	decay := float32(math.Exp(float64(-touchGlideFriction * dt)))
	p.vx *= decay
	p.vy *= decay
	if math.Hypot(float64(p.vx), float64(p.vy)) < 20 {
		p.gliding = false
		return
	}
	rt.requestFrame()
}

// touchScrolls reports whether a finger moving at x, y scrolls: there is
// content that scrolls under it, and nothing nearer the finger takes the
// pointer's moves of its own, as a canvas, text, or what drags does.
func (rt *engine) touchScrolls(x, y float32) bool {
	chain := rt.appendHitChain(nil, x, y)
	for _, id := range chain {
		s := rt.states[id]
		if s == nil {
			continue
		}
		// What drags a value to drop elsewhere (Drag), as a card or a
		// row, does not keep a quick finger from scrolling: a finger held
		// still drags it, as on a phone. What moves of its own as the
		// finger does (Draggable, a slider, a canvas) takes it at once.
		valueOnly := s.drags() && s.input == nil && s.flags&(flagEditable|flagSelectable|flagTrackPointer|flagDragWindow) == 0
		if !valueOnly && (s.input != nil || s.flags&(flagEditable|flagSelectable|flagDraggable|flagTrackPointer|flagDragWindow) != 0) {
			return false
		}
		if s.flags&(flagScrollX|flagScrollY) != 0 {
			return true
		}
	}
	return false
}
