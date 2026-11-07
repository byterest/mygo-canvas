//go:build ios

package ios

import (
	"unicode"

	"github.com/egoist/mygo/internal/platform"
)

// presses reports the keys of a hardware keyboard pressed or released, and
// the text a press types, and whether any of the set was a key.
func (s *surface) presses(set id, down bool) bool {
	took := false
	eachTouch(set, func(p id) {
		key := send(p, "key")
		if key == 0 {
			return
		}
		took = true
		code := sendInt(key, "keyCode")
		flags := sendInt(key, "modifierFlags")
		var mods platform.Modifiers
		if flags&(1<<17) != 0 {
			mods |= platform.ModShift
		}
		if flags&(1<<18) != 0 {
			mods |= platform.ModCtrl
		}
		if flags&(1<<19) != 0 {
			mods |= platform.ModAlt
		}
		if flags&(1<<20) != 0 {
			mods |= platform.ModSuper
		}
		k := hidKey(code)
		if !down {
			s.send(platform.SurfaceEvent{Kind: platform.KeyReleased, Key: k, PhysicalKey: k, Mods: mods})
			return
		}
		if s.send(platform.SurfaceEvent{Kind: platform.KeyPressed, Key: k, PhysicalKey: k, Mods: mods}) {
			return // a shortcut took it
		}
		if mods&(platform.ModCtrl|platform.ModSuper) != 0 || !s.input.Active {
			return
		}
		text := goString(send(key, "characters"))
		printable := text != ""
		for _, r := range text {
			if !unicode.IsPrint(r) {
				printable = false
			}
		}
		if printable {
			s.send(platform.SurfaceEvent{Kind: platform.TextInput, Text: text})
		}
	})
	return took
}

// hidKey returns the key of a USB HID usage of the keyboard page.
func hidKey(code int) platform.Key {
	switch {
	case code >= 0x04 && code <= 0x1d:
		return platform.KeyA + platform.Key(code-0x04)
	case code >= 0x1e && code <= 0x26:
		return platform.Key1 + platform.Key(code-0x1e)
	case code == 0x27:
		return platform.Key0
	case code >= 0x3a && code <= 0x45:
		return platform.KeyF1 + platform.Key(code-0x3a)
	}
	switch code {
	case 0x28, 0x58:
		return platform.KeyEnter
	case 0x29:
		return platform.KeyEscape
	case 0x2a:
		return platform.KeyBackspace
	case 0x2b:
		return platform.KeyTab
	case 0x2c:
		return platform.KeySpace
	case 0x2d:
		return platform.KeyMinus
	case 0x2e:
		return platform.KeyEqual
	case 0x2f:
		return platform.KeyBracketLeft
	case 0x30:
		return platform.KeyBracketRight
	case 0x31:
		return platform.KeyBackslash
	case 0x33:
		return platform.KeySemicolon
	case 0x34:
		return platform.KeyQuote
	case 0x35:
		return platform.KeyBackquote
	case 0x36:
		return platform.KeyComma
	case 0x37:
		return platform.KeyPeriod
	case 0x38:
		return platform.KeySlash
	case 0x49:
		return platform.KeyInsert
	case 0x4a:
		return platform.KeyHome
	case 0x4b:
		return platform.KeyPageUp
	case 0x4c:
		return platform.KeyDelete
	case 0x4d:
		return platform.KeyEnd
	case 0x4e:
		return platform.KeyPageDown
	case 0x4f:
		return platform.KeyRight
	case 0x50:
		return platform.KeyLeft
	case 0x51:
		return platform.KeyDown
	case 0x52:
		return platform.KeyUp
	}
	return platform.KeyUnknown
}
