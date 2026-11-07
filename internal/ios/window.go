//go:build ios

package ios

import (
	"errors"

	"github.com/egoist/mygo/internal/platform"
)

// window is a UIWindow filling the screen, its root view controller's view
// the surface. iOS has one window an app shows at a time on a phone, and a
// window a scene on a tablet: most of what a desktop's window does is
// inert.
type window struct {
	noPage
	b       *Backend
	h       platform.WindowHandler
	win, vc id
	surface *surface
	title   string
	visible bool
	closed  bool
}

func (b *Backend) NewWindow(opts *platform.WindowOptions, h platform.WindowHandler) (platform.Window, error) {
	if !opts.Surface {
		return nil, errors.New("ios: windows show native UI only (WindowOptions.Content)")
	}
	r, _ := mainScreen()
	w := &window{b: b, h: h, title: opts.Title}
	if b.scene != 0 {
		w.win = send(send(class("UIWindow"), "alloc"), "initWithWindowScene:", b.scene)
	} else {
		w.win = msgInitRect(send(class("UIWindow"), "alloc"), sel("initWithFrame:"), r)
	}
	w.vc = send(send(class("MyGoViewController"), "alloc"), "init")
	w.surface = newSurface(w, r)
	send(w.vc, "setView:", w.surface.view)
	send(w.win, "setRootViewController:", w.vc)
	send(w.win, "setBackgroundColor:", send(class("UIColor"), "systemBackgroundColor"))
	b.windows = append(b.windows, w)
	return w, nil
}

func (w *window) Handle() uintptr { return uintptr(w.win) }

func (w *window) Surface() platform.Surface {
	if w.surface == nil {
		return nil
	}
	return w.surface
}

func (w *window) SetTitle(title string)     { w.title = title }
func (w *window) Title() string             { return w.title }
func (w *window) SetRepresentedFile(string) {}
func (w *window) SetDocumentEdited(bool)    {}

func (w *window) bounds() platform.Rect {
	r := msgRect(w.win, sel("bounds"))
	return platform.Rect{Width: int(r.Size.W), Height: int(r.Size.H)}
}

func (w *window) SetBounds(platform.Rect)        {}
func (w *window) Bounds() platform.Rect          { return w.bounds() }
func (w *window) SetContentBounds(platform.Rect) {}
func (w *window) ContentBounds() platform.Rect   { return w.bounds() }
func (w *window) SetMinimumSize(platform.Size)   {}
func (w *window) SetMaximumSize(platform.Size)   {}
func (w *window) SetResizable(bool)              {}
func (w *window) IsResizable() bool              { return false }
func (w *window) SetMovable(bool)                {}
func (w *window) IsMovable() bool                { return false }
func (w *window) SetMinimizable(bool)            {}
func (w *window) IsMinimizable() bool            { return false }
func (w *window) SetMaximizable(bool)            {}
func (w *window) IsMaximizable() bool            { return false }
func (w *window) SetClosable(bool)               {}
func (w *window) IsClosable() bool               { return false }
func (w *window) SetAlwaysOnTop(bool)            {}
func (w *window) IsAlwaysOnTop() bool            { return false }

func (w *window) Show() {
	if w.closed {
		return
	}
	w.visible = true
	if w.b.scene == 0 {
		return // shown when its scene connects
	}
	w.present()
}

// present makes the window key and visible in its scene.
func (w *window) present() {
	send(w.win, "makeKeyAndVisible")
	send(w.surface.view, "becomeFirstResponder")
	w.h.Focused()
}

// attach puts a window made before the scene connected in it, and shows
// it if it was asked to.
func (w *window) attach(scene id) {
	if w.closed {
		return
	}
	send(w.win, "setWindowScene:", scene)
	msgSetRect(w.win, sel("setFrame:"), msgRect(send(scene, "coordinateSpace"), sel("bounds")))
	if w.visible {
		w.present()
	}
}

func (w *window) ShowInactive() { w.Show() }

func (w *window) Hide() {
	send(w.win, "setHidden:", true)
	w.visible = false
}

func (w *window) IsVisible() bool                   { return w.visible }
func (w *window) Focus()                            { w.Show() }
func (w *window) Blur()                             {}
func (w *window) IsFocused() bool                   { return w.visible }
func (w *window) Minimize()                         {}
func (w *window) IsMinimized() bool                 { return false }
func (w *window) Maximize()                         {}
func (w *window) Unmaximize()                       {}
func (w *window) IsMaximized() bool                 { return true }
func (w *window) Restore()                          {}
func (w *window) SetFullScreen(bool)                {}
func (w *window) IsFullScreen() bool                { return true }
func (w *window) Center()                           {}
func (w *window) SetBackgroundColor(platform.Color) {}
func (w *window) SetOpacity(float64)                {}
func (w *window) Opacity() float64                  { return 1 }
func (w *window) SetHasShadow(bool)                 {}
func (w *window) HasShadow() bool                   { return false }
func (w *window) SetIgnoreMouseEvents(bool)         {}
func (w *window) SetContentProtection(bool)         {}
func (w *window) SetVibrancy(string)                {}
func (w *window) SetActiveAppearance(bool)          {}
func (w *window) SetWindowButtonsTheme(string)      {}
func (w *window) SetProgressBar(string, float64)    {}
func (w *window) FlashFrame(bool)                   {}
func (w *window) SetSkipTaskbar(bool)               {}
func (w *window) SetVisibleOnAllWorkspaces(bool)    {}
func (w *window) SetIcon([]byte) error              { return nil }
func (w *window) SetMenu(*platform.Menu)            {}
func (w *window) SetAutoHideMenu(bool)              {}

// TitleBar is the room the status bar takes at the top of the screen, and
// under it a band as tall as a navigation bar, which the content's own bar
// takes, with nothing of the system's in it.
func (w *window) TitleBar() platform.TitleBar {
	in := msgInsets(w.surface.view, sel("safeAreaInsets"))
	return platform.TitleBar{Top: int(in.Top), Height: navigationBarHeight}
}

// navigationBarHeight is how tall UIKit's navigation bars are.
const navigationBarHeight = 44

func (w *window) StartDrag()                                 {}
func (w *window) TitleBarDoubleClicked()                     {}
func (w *window) PlaceFlyout(platform.Flyout, platform.Size) {}

func (w *window) Close() {
	if w.closed {
		return
	}
	w.Hide()
	w.closed = true
	w.surface.destroy()
	w.h.Closed()
}
