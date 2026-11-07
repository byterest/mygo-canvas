//go:build ios

package ios

import (
	"reflect"
	"unicode"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
)

// The on-screen keyboard shows for a first responder that takes text. The
// surface is not one, so that the keys of a hardware keyboard reach it
// without a keyboard on screen; while an input is active, a transparent
// child of it that is one has the keyboard instead.
//
// The child is a UITextInput, so that input methods compose (pinyin,
// handwriting): its document is the focused element's, through its
// TextInputClient, or else the text around the caret TextInputState gives
// with the composition in place of the selection, as on macOS. Positions
// count UTF-16 units of that document.

// ime is a surface's state of text input.
type ime struct {
	marked    string // the composition, without a TextInputClient
	markedSel _NSRange
	delegate  id // UIKit's input delegate, told of changes not its own
	tokenizer id
	ours      bool // the last change of the text came from the keyboard

	keyboardTop float64        // where the keyboard starts, in points of the view
	scrolledAt  platform.RectF // the caret the last scroll to show it started from
}

// _NSRange mirrors NSRange; its name is its Objective-C encoding's.
type _NSRange struct{ Location, Length uint }

// newTextView makes the surface's text view.
func (s *surface) newTextView() {
	s.text = msgInitRect(send(class("MyGoTextView"), "alloc"), sel("initWithFrame:"), CGRect{Size: CGSize{1, 1}})
	send(s.text, "setUserInteractionEnabled:", false)
	send(s.text, "setBackgroundColor:", send(class("UIColor"), "clearColor"))
	send(s.view, "addSubview:", s.text)
	s.w.b.byText[s.text] = s
	nc := send(class("NSNotificationCenter"), "defaultCenter")
	for _, name := range []uintptr{keyboardWillChangeFrame, keyboardWillHide} {
		if name != 0 {
			send(nc, "addObserver:selector:name:object:", s.text, sel("mygoKeyboard:"), name, 0)
		}
	}
}

// SetTextInput takes the state of the input that has the keyboard: the
// keyboard shows while one is active, and goes when none is. A change of
// the text not typed tells the input methods to read it again.
func (s *surface) SetTextInput(t platform.TextInputState) {
	old := s.input
	s.input = t
	if s.text == 0 || !s.w.visible {
		return
	}
	switch {
	case t.Active && !old.Active:
		s.ime.marked = ""
		send(s.text, "becomeFirstResponder")
	case !t.Active && old.Active:
		s.ime.marked = ""
		send(s.view, "becomeFirstResponder")
	case t.Active && !s.ime.ours && (t.Text != old.Text || t.Start != old.Start || t.End != old.End):
		if d := s.ime.delegate; d != 0 {
			send(d, "selectionWillChange:", s.text)
			send(d, "textWillChange:", s.text)
			send(d, "textDidChange:", s.text)
			send(d, "selectionDidChange:", s.text)
		}
	}
	s.ime.ours = false
	s.keepCaretVisible()
}

// typing reports whether the text view has the keyboard.
func (s *surface) typing() bool {
	return s.text != 0 && s.input.Active && sendBool(s.text, "isFirstResponder")
}

// key presses and releases a key, as the on-screen keyboard has no
// presses of its own.
func (s *surface) key(k platform.Key) {
	s.send(platform.SurfaceEvent{Kind: platform.KeyPressed, Key: k, PhysicalKey: k})
	s.send(platform.SurfaceEvent{Kind: platform.KeyReleased, Key: k, PhysicalKey: k})
}

// textPresses takes the presses of a hardware keyboard while the text
// view has it, and reports whether they are done with. Keys go to the
// content first, for its shortcuts; what they type goes on to the text
// system, which types it, or composes it with an input method, which has
// all keys while it composes.
func (s *surface) textPresses(set id, down bool) bool {
	if s.composing() {
		return false
	}
	done := true
	eachTouch(set, func(p id) {
		key := send(p, "key")
		if key == 0 {
			done = false
			return
		}
		k, mods := hidKey(sendInt(key, "keyCode")), keyMods(sendInt(key, "modifierFlags"))
		if !down {
			s.send(platform.SurfaceEvent{Kind: platform.KeyReleased, Key: k, PhysicalKey: k, Mods: mods})
			return
		}
		if s.send(platform.SurfaceEvent{Kind: platform.KeyPressed, Key: k, PhysicalKey: k, Mods: mods}) {
			return
		}
		if mods&(platform.ModCtrl|platform.ModSuper) != 0 {
			return
		}
		text := goString(send(key, "charactersIgnoringModifiers"))
		for _, r := range text {
			if unicode.IsPrint(r) {
				done = false // the text system types it
				return
			}
		}
	})
	return done
}

// --- the keyboard's place ---

// keyboard takes where the keyboard will be, from a notification of it.
func (s *surface) keyboard(note id) {
	b := msgRect(s.view, sel("bounds"))
	s.ime.keyboardTop = b.Size.H
	if goString(send(note, "name")) != goString(id(keyboardWillHide)) {
		if v := send(send(note, "userInfo"), "objectForKey:", keyboardFrameEnd); v != 0 {
			r := msgConvertRect(s.view, sel("convertRect:fromView:"), msgRect(v, sel("CGRectValue")), 0)
			if r.Origin.Y < b.Size.H {
				s.ime.keyboardTop = r.Origin.Y
			}
		}
	}
	s.ime.scrolledAt = platform.RectF{}
	s.keepCaretVisible()
}

// caretMargin is the room left between the caret and the keyboard.
const caretMargin = 24

// keepCaretVisible scrolls what is at the caret up when the keyboard
// covers it, as a finger would: a canvas pans, a list scrolls. A scroll
// that moved nothing is not tried again from the same place.
func (s *surface) keepCaretVisible() {
	c := s.input.Caret
	top := s.ime.keyboardTop
	if !s.input.Active || top <= 0 || c.H <= 0 {
		return
	}
	over := c.Y + c.H - (top - caretMargin)
	if over <= 0 || c == s.ime.scrolledAt {
		return
	}
	s.ime.scrolledAt = c
	s.send(platform.SurfaceEvent{Kind: platform.PointerScroll, X: c.X + c.W/2, Y: c.Y + c.H/2, DY: -over, Precise: true})
}

// --- the document ---

// document returns the text input methods see without a client, and where
// the selection, or the composition, starts and ends in it, in runes.
func (s *surface) document() (doc []rune, start, end int) {
	text := []rune(s.input.Text)
	start = max(0, min(s.input.Start, len(text)))
	end = max(start, min(s.input.End, len(text)))
	if s.ime.marked == "" {
		return text, start, end
	}
	m := []rune(s.ime.marked)
	doc = make([]rune, 0, len(text)-(end-start)+len(m))
	doc = append(append(append(doc, text[:start]...), m...), text[end:]...)
	return doc, start, start + len(m)
}

func units(runes []rune) int {
	n := 0
	for _, r := range runes {
		n += utf16.RuneLen(r)
	}
	return n
}

// runeAt returns the rune of runes that UTF-16 unit u starts, or the end.
func runeAt(runes []rune, u int) int {
	for i, r := range runes {
		if u <= 0 {
			return i
		}
		u -= utf16.RuneLen(r)
	}
	return len(runes)
}

func (s *surface) docLen() int {
	if c := s.input.Client; c != nil {
		_, r := c.TextForRange(platform.TextRange{Start: 0, End: 1 << 30})
		return r.End
	}
	doc, _, _ := s.document()
	return units(doc)
}

func (s *surface) docText(r platform.TextRange) string {
	if c := s.input.Client; c != nil {
		t, _ := c.TextForRange(r)
		return t
	}
	doc, _, _ := s.document()
	u := utf16.Encode(doc)
	lo := max(0, min(r.Start, len(u)))
	hi := max(lo, min(r.End, len(u)))
	return string(utf16.Decode(u[lo:hi]))
}

func (s *surface) docSelection() platform.TextRange {
	if c := s.input.Client; c != nil {
		return platform.NormalizeTextRange(c.Selection().Range)
	}
	doc, start, end := s.document()
	if s.ime.marked != "" {
		lo := units(doc[:start]) + int(s.ime.markedSel.Location)
		return platform.TextRange{Start: lo, End: lo + int(s.ime.markedSel.Length)}
	}
	return platform.TextRange{Start: units(doc[:start]), End: units(doc[:end])}
}

func (s *surface) docMarked() (platform.TextRange, bool) {
	if !s.input.Active {
		return platform.TextRange{}, false
	}
	if c := s.input.Client; c != nil {
		return c.MarkedRange()
	}
	if s.ime.marked == "" {
		return platform.TextRange{}, false
	}
	doc, start, end := s.document()
	return platform.TextRange{Start: units(doc[:start]), End: units(doc[:end])}, true
}

// composing reports whether an input method has a composition.
func (s *surface) composing() bool {
	_, ok := s.docMarked()
	return ok
}

// textRange converts a range of the document without a client into runes
// of TextInputState.Text: a range in the composition stands for the
// selection it replaces.
func (s *surface) textRange(r platform.TextRange) (from, to int) {
	doc, start, end := s.document()
	lo, hi := runeAt(doc, r.Start), runeAt(doc, r.End)
	if s.ime.marked == "" {
		return lo, hi
	}
	n := len([]rune(s.input.Text))
	sel := max(0, min(s.input.End, n)) - max(0, min(s.input.Start, n))
	conv := func(i int, after bool) int {
		switch {
		case i <= start:
			return i
		case i < end:
			if after {
				return start + sel
			}
			return start
		}
		return i - (end - start) + sel
	}
	return conv(lo, false), conv(hi, true)
}

// setMarked composes text over the composition, or the selection, with
// sel selected in it.
func (s *surface) setMarked(text string, sel _NSRange) {
	s.ime.ours = true
	if c := s.input.Client; c != nil {
		if text == "" {
			c.ReplaceText(nil, "")
			return
		}
		c.SetMarkedText(nil, text, platform.TextRange{Start: int(sel.Location), End: int(sel.Location + sel.Length)})
		return
	}
	caret := 0
	for u, r := 0, []rune(text); caret < len(r) && u < int(sel.Location); caret++ {
		u += utf16.RuneLen(r[caret])
	}
	s.ime.marked, s.ime.markedSel = text, sel
	s.send(platform.SurfaceEvent{Kind: platform.TextComposition, Text: text, Caret: caret})
}

// replace puts text in place of r, or of the composition or the
// selection when r is nil, ending the composition.
func (s *surface) replace(r *platform.TextRange, text string) {
	s.ime.ours = true
	if c := s.input.Client; c != nil {
		c.ReplaceText(r, text)
		return
	}
	ev := platform.SurfaceEvent{Kind: platform.TextInput, Text: text}
	if r != nil {
		ev.Replace = true
		ev.From, ev.To = s.textRange(*r)
	}
	if s.ime.marked != "" {
		s.ime.marked = ""
		s.send(platform.SurfaceEvent{Kind: platform.TextComposition})
	}
	if text != "" || ev.Replace {
		s.send(ev)
	}
}

// unmark ends the composition, which stays as typed.
func (s *surface) unmark() {
	s.ime.ours = true
	if c := s.input.Client; c != nil {
		c.UnmarkText()
		return
	}
	if s.ime.marked != "" {
		s.replace(nil, s.ime.marked)
	}
}

// rectOf returns the rectangle of a range of the document, in points of
// the view: the caret's where the content does not know.
func (s *surface) rectOf(r platform.TextRange) CGRect {
	f := s.input.Caret
	if c := s.input.Client; c != nil {
		if b, _, ok := c.BoundsForRange(r); ok {
			f = b
		}
	}
	return CGRect{Origin: CGPoint{f.X, f.Y}, Size: CGSize{f.W, f.H}}
}

func (s *surface) indexAt(p CGPoint) int {
	if c := s.input.Client; c != nil {
		if i, ok := c.IndexForPoint(p.X, p.Y); ok {
			return i
		}
	}
	return s.docSelection().End
}

// --- positions and ranges ---

var posOffset, rangeLo, rangeHi uintptr

// ivar returns the int at an offset of an object.
func ivar(obj id, off uintptr) *int {
	return (*int)(unsafe.Add(*(*unsafe.Pointer)(unsafe.Pointer(&obj)), off))
}

func newPos(o int) id {
	p := send(send(class("MyGoTextPosition"), "alloc"), "init")
	*ivar(p, posOffset) = o
	return send(p, "autorelease")
}

func posOf(p id) int {
	if p == 0 {
		return 0
	}
	return *ivar(p, posOffset)
}

func newRange(r platform.TextRange) id {
	t := send(send(class("MyGoTextRange"), "alloc"), "init")
	*ivar(t, rangeLo) = r.Start
	*ivar(t, rangeHi) = r.End
	return send(t, "autorelease")
}

func rangeOf(t id) platform.TextRange {
	if t == 0 {
		return platform.TextRange{}
	}
	return platform.NormalizeTextRange(platform.TextRange{Start: *ivar(t, rangeLo), End: *ivar(t, rangeHi)})
}

// UITextStorageDirection and UITextLayoutDirection, which share values.
const (
	dirForward  = 0
	dirBackward = 1
	dirRight    = 2
	dirLeft     = 3
	dirUp       = 4
)

func backward(dir int) bool { return dir == dirBackward || dir == dirLeft || dir == dirUp }

func registerTextViewClass() {
	intType := reflect.TypeFor[int]()
	posClass, err := objc.RegisterClass("MyGoTextPosition", objc.GetClass("UITextPosition"), nil,
		[]objc.FieldDef{{Name: "offset", Type: intType, Attribute: objc.ReadOnly}}, nil)
	if err != nil {
		panic(err)
	}
	posOffset = posClass.InstanceVariable("offset").Offset()
	rangeClass, err := objc.RegisterClass("MyGoTextRange", objc.GetClass("UITextRange"), nil,
		[]objc.FieldDef{{Name: "lo", Type: intType, Attribute: objc.ReadOnly}, {Name: "hi", Type: intType, Attribute: objc.ReadOnly}},
		[]objc.MethodDef{
			{Cmd: sel("start"), Fn: func(self id, _ objc.SEL) id { return newPos(rangeOf(self).Start) }},
			{Cmd: sel("end"), Fn: func(self id, _ objc.SEL) id { return newPos(rangeOf(self).End) }},
			{Cmd: sel("isEmpty"), Fn: func(self id, _ objc.SEL) bool { r := rangeOf(self); return r.Start == r.End }},
		})
	if err != nil {
		panic(err)
	}
	rangeLo = rangeClass.InstanceVariable("lo").Offset()
	rangeHi = rangeClass.InstanceVariable("hi").Offset()

	textOf := func(self id) *surface { return theBackend.byText[self] }
	clamp := func(s *surface, o int) int { return max(0, min(o, s.docLen())) }
	_, err = objc.RegisterClass("MyGoTextView", objc.GetClass("UIView"),
		[]*objc.Protocol{objc.GetProtocol("UITextInput")}, nil,
		[]objc.MethodDef{
			{Cmd: sel("canBecomeFirstResponder"), Fn: func(self id, _ objc.SEL) bool { return true }},
			{Cmd: sel("mygoKeyboard:"), Fn: func(self id, _ objc.SEL, note id) {
				if s := textOf(self); s != nil {
					s.keyboard(note)
				}
			}},

			// UIKeyInput
			{Cmd: sel("hasText"), Fn: func(self id, _ objc.SEL) bool {
				s := textOf(self)
				return s != nil && s.docLen() > 0
			}},
			{Cmd: sel("insertText:"), Fn: func(self id, _ objc.SEL, text id) {
				s := textOf(self)
				if s == nil {
					return
				}
				t := goString(text)
				switch composing := s.composing(); {
				case t == "\n" && !composing:
					s.key(platform.KeyEnter)
				case t == "\t" && !composing:
					s.key(platform.KeyTab)
				default:
					s.replace(nil, t)
				}
			}},
			{Cmd: sel("deleteBackward"), Fn: func(self id, _ objc.SEL) {
				s := textOf(self)
				if s == nil {
					return
				}
				if m, ok := s.docMarked(); ok {
					s.replace(&m, "")
					return
				}
				s.key(platform.KeyBackspace)
			}},

			// UITextInput: the text
			{Cmd: sel("textInRange:"), Fn: func(self id, _ objc.SEL, r id) id {
				s := textOf(self)
				if s == nil {
					return 0
				}
				return nsString(s.docText(rangeOf(r)))
			}},
			{Cmd: sel("replaceRange:withText:"), Fn: func(self id, _ objc.SEL, r, text id) {
				if s := textOf(self); s != nil {
					rr := rangeOf(r)
					s.replace(&rr, goString(text))
				}
			}},
			{Cmd: sel("selectedTextRange"), Fn: func(self id, _ objc.SEL) id {
				s := textOf(self)
				if s == nil || !s.input.Active {
					return 0
				}
				return newRange(s.docSelection())
			}},
			// The content keeps its selection: the keyboard's moves of the
			// caret (the space bar dragged) come as its arrows would.
			{Cmd: sel("setSelectedTextRange:"), Fn: func(self id, _ objc.SEL, r id) {
				s := textOf(self)
				if s == nil || r == 0 || s.composing() {
					return
				}
				cur, want := s.docSelection(), rangeOf(r)
				if want.Start != want.End || cur.Start != cur.End {
					return
				}
				s.ime.ours = true
				for ; cur.End < want.End; cur.End++ {
					s.key(platform.KeyRight)
				}
				for ; cur.End > want.End; cur.End-- {
					s.key(platform.KeyLeft)
				}
			}},
			{Cmd: sel("markedTextRange"), Fn: func(self id, _ objc.SEL) id {
				s := textOf(self)
				if s == nil {
					return 0
				}
				if m, ok := s.docMarked(); ok {
					return newRange(m)
				}
				return 0
			}},
			{Cmd: sel("markedTextStyle"), Fn: func(self id, _ objc.SEL) id { return 0 }},
			{Cmd: sel("setMarkedTextStyle:"), Fn: func(self id, _ objc.SEL, style id) {}},
			{Cmd: sel("setMarkedText:selectedRange:"), Fn: func(self id, _ objc.SEL, text id, r _NSRange) {
				if s := textOf(self); s != nil {
					s.setMarked(goString(text), r)
				}
			}},
			{Cmd: sel("unmarkText"), Fn: func(self id, _ objc.SEL) {
				if s := textOf(self); s != nil {
					s.unmark()
				}
			}},

			// UITextInput: positions
			{Cmd: sel("beginningOfDocument"), Fn: func(self id, _ objc.SEL) id { return newPos(0) }},
			{Cmd: sel("endOfDocument"), Fn: func(self id, _ objc.SEL) id {
				if s := textOf(self); s != nil {
					return newPos(s.docLen())
				}
				return newPos(0)
			}},
			{Cmd: sel("textRangeFromPosition:toPosition:"), Fn: func(self id, _ objc.SEL, a, b id) id {
				return newRange(platform.NormalizeTextRange(platform.TextRange{Start: posOf(a), End: posOf(b)}))
			}},
			{Cmd: sel("positionFromPosition:offset:"), Fn: func(self id, _ objc.SEL, p id, off int) id {
				s := textOf(self)
				o := posOf(p) + off
				if s == nil || o < 0 || o > s.docLen() {
					return 0
				}
				return newPos(o)
			}},
			{Cmd: sel("positionFromPosition:inDirection:offset:"), Fn: func(self id, _ objc.SEL, p id, dir, off int) id {
				s := textOf(self)
				if s == nil || dir > dirLeft {
					return 0
				}
				if backward(dir) {
					off = -off
				}
				return newPos(clamp(s, posOf(p)+off))
			}},
			{Cmd: sel("comparePosition:toPosition:"), Fn: func(self id, _ objc.SEL, a, b id) int {
				switch x, y := posOf(a), posOf(b); {
				case x < y:
					return -1
				case x > y:
					return 1
				}
				return 0
			}},
			{Cmd: sel("offsetFromPosition:toPosition:"), Fn: func(self id, _ objc.SEL, a, b id) int { return posOf(b) - posOf(a) }},
			{Cmd: sel("positionWithinRange:farthestInDirection:"), Fn: func(self id, _ objc.SEL, r id, dir int) id {
				if rr := rangeOf(r); !backward(dir) {
					return newPos(rr.End)
				} else {
					return newPos(rr.Start)
				}
			}},
			{Cmd: sel("characterRangeByExtendingPosition:inDirection:"), Fn: func(self id, _ objc.SEL, p id, dir int) id {
				s := textOf(self)
				if s == nil {
					return 0
				}
				o := posOf(p)
				if backward(dir) {
					return newRange(platform.TextRange{Start: clamp(s, o-1), End: o})
				}
				return newRange(platform.TextRange{Start: o, End: clamp(s, o+1)})
			}},
			{Cmd: sel("baseWritingDirectionForPosition:inDirection:"), Fn: func(self id, _ objc.SEL, p id, dir int) int { return 0 }},
			{Cmd: sel("setBaseWritingDirection:forRange:"), Fn: func(self id, _ objc.SEL, wd int, r id) {}},

			// UITextInput: geometry, in points of the view, which is at the
			// surface's origin
			{Cmd: sel("firstRectForRange:"), Fn: func(self id, _ objc.SEL, r id) CGRect {
				if s := textOf(self); s != nil {
					return s.rectOf(rangeOf(r))
				}
				return CGRect{}
			}},
			{Cmd: sel("caretRectForPosition:"), Fn: func(self id, _ objc.SEL, p id) CGRect {
				s := textOf(self)
				if s == nil {
					return CGRect{}
				}
				o := posOf(p)
				r := s.rectOf(platform.TextRange{Start: o, End: o})
				r.Size.W = 2
				return r
			}},
			{Cmd: sel("selectionRectsForRange:"), Fn: func(self id, _ objc.SEL, r id) id { return send(class("NSArray"), "array") }},
			{Cmd: sel("closestPositionToPoint:"), Fn: func(self id, _ objc.SEL, p CGPoint) id {
				if s := textOf(self); s != nil {
					return newPos(s.indexAt(p))
				}
				return newPos(0)
			}},
			{Cmd: sel("closestPositionToPoint:withinRange:"), Fn: func(self id, _ objc.SEL, p CGPoint, r id) id {
				s, rr := textOf(self), rangeOf(r)
				if s == nil {
					return newPos(rr.Start)
				}
				return newPos(max(rr.Start, min(s.indexAt(p), rr.End)))
			}},
			{Cmd: sel("characterRangeAtPoint:"), Fn: func(self id, _ objc.SEL, p CGPoint) id {
				s := textOf(self)
				if s == nil {
					return 0
				}
				i := s.indexAt(p)
				return newRange(platform.TextRange{Start: i, End: clamp(s, i+1)})
			}},

			// UITextInput: the system's helpers
			{Cmd: sel("inputDelegate"), Fn: func(self id, _ objc.SEL) id {
				if s := textOf(self); s != nil {
					return s.ime.delegate
				}
				return 0
			}},
			{Cmd: sel("setInputDelegate:"), Fn: func(self id, _ objc.SEL, d id) {
				if s := textOf(self); s != nil {
					s.ime.delegate = d
				}
			}},
			{Cmd: sel("tokenizer"), Fn: func(self id, _ objc.SEL) id {
				s := textOf(self)
				if s == nil {
					return 0
				}
				if s.ime.tokenizer == 0 {
					s.ime.tokenizer = send(send(class("UITextInputStringTokenizer"), "alloc"), "initWithTextInput:", self)
				}
				return s.ime.tokenizer
			}},

			// Plain text: no corrections, capitals or spelling, which would
			// rewrite words behind the content's back.
			{Cmd: sel("autocorrectionType"), Fn: func(self id, _ objc.SEL) int { return 1 }},
			{Cmd: sel("autocapitalizationType"), Fn: func(self id, _ objc.SEL) int { return 0 }},
			{Cmd: sel("spellCheckingType"), Fn: func(self id, _ objc.SEL) int { return 1 }},
			{Cmd: sel("smartQuotesType"), Fn: func(self id, _ objc.SEL) int { return 1 }},
			{Cmd: sel("smartDashesType"), Fn: func(self id, _ objc.SEL) int { return 1 }},
			{Cmd: sel("smartInsertDeleteType"), Fn: func(self id, _ objc.SEL) int { return 1 }},
			{Cmd: sel("inlinePredictionType"), Fn: func(self id, _ objc.SEL) int { return 1 }},

			{Cmd: sel("pressesBegan:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
				if s := textOf(self); s == nil || !s.textPresses(set, true) {
					self.SendSuper(sel("pressesBegan:withEvent:"), set, ev)
				}
			}},
			{Cmd: sel("pressesEnded:withEvent:"), Fn: func(self id, _ objc.SEL, set, ev id) {
				if s := textOf(self); s == nil || !s.textPresses(set, false) {
					self.SendSuper(sel("pressesEnded:withEvent:"), set, ev)
				}
			}},
		})
	if err != nil {
		panic(err)
	}
}
