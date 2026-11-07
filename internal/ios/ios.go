//go:build ios

// Package ios implements the iOS and iPadOS backend (UIKit) in Go:
// Objective-C is driven through the runtime with purego. Windows show
// native UI (package ui) only: a window fills its screen, and its surface
// takes touches, the Apple Pencil, a hardware keyboard and pinches.
//
// What a phone or a tablet has no counterpart of (menus, trays, hotkeys,
// the Dock, windows' frames) is inert, as the unsupported backend's.
package ios

import (
	"errors"
	"os"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/unsupported"
)

// Backend is the UIKit backend.
type Backend struct {
	*unsupported.Backend

	h        platform.AppHandler
	source   uintptr // the run loop source Signal fires
	mainLoop uintptr
	launched bool

	windows   []*window
	bySurface map[id]*surface
	byText    map[id]*surface
	scene     id // the window scene, once UIKit connects it
}

// New returns the UIKit backend.
func New() *Backend {
	// The core asks which thread it is on before Init.
	load()
	return &Backend{Backend: unsupported.New(), bySurface: map[id]*surface{}, byText: map[id]*surface{}}
}

var theBackend *Backend

// cfRunLoopSourceContext mirrors CFRunLoopSourceContext (version 0).
type cfRunLoopSourceContext struct {
	version                                       int
	info                                          uintptr
	retain, release, copyDescription, equal, hash uintptr
	schedule, cancel, perform                     uintptr
}

var performCallback uintptr

func (b *Backend) Name() string { return "ios/uikit" }

func (b *Backend) Init(h platform.AppHandler, opts platform.AppOptions) error {
	if err := load(); err != nil {
		return err
	}
	b.h = h
	theBackend = b
	registerClasses()
	// Signal fires a source of the main run loop, whose callback drains
	// the core's queue there, as on macOS.
	if performCallback == 0 {
		performCallback = purego.NewCallback(func(info uintptr) uintptr {
			if theBackend != nil && theBackend.h != nil {
				theBackend.h.Dispatch()
			}
			return 0
		})
	}
	ctx := cfRunLoopSourceContext{perform: performCallback}
	b.mainLoop, _, _ = purego.SyscallN(cfRunLoopGetMain)
	b.source, _, _ = purego.SyscallN(cfRunLoopSourceCreate, 0, 0, uintptr(unsafe.Pointer(&ctx)))
	purego.SyscallN(cfRunLoopAddSource, b.mainLoop, b.source, kCFRunLoopCommonModes)
	return nil
}

// Run hands the main thread to UIKit, which never gives it back: an app
// of iOS ends when the system ends it.
func (b *Backend) Run() error {
	var argv [1]uintptr
	purego.SyscallN(uiApplicationMain, 0, uintptr(unsafe.Pointer(&argv[0])), 0, uintptr(nsString("MyGoAppDelegate")))
	return errors.New("ios: UIApplicationMain returned")
}

// Quit ends the process: iOS apps do not quit themselves, but an app
// asked to may as well go.
func (b *Backend) Quit() { os.Exit(0) }

func (b *Backend) IsMainThread() bool {
	r, _, _ := purego.SyscallN(pthreadMainNP)
	return int32(r) != 0
}

func (b *Backend) Signal() {
	if b.source != 0 {
		purego.SyscallN(cfRunLoopSourceSignal, b.source)
		purego.SyscallN(cfRunLoopWakeUp, b.mainLoop)
	}
}

// Step runs the main run loop until a source fires, for at most half a
// second. CFRunLoopRunInMode takes its seconds as a double, which purego
// passes in a floating point register only through a typed function.
func (b *Backend) Step() { runLoopRunInMode(kCFRunLoopDefaultMode, 0.5, true) }

var runLoopRunInMode func(mode uintptr, seconds float64, returnAfterSource bool) int32

// Wake makes a pending Step return.
func (b *Backend) Wake() {
	if b.mainLoop != 0 {
		purego.SyscallN(cfRunLoopWakeUp, b.mainLoop)
	}
}

func (b *Backend) Screen() platform.Screen { return screen{} }
func (b *Backend) Theme() platform.Theme   { return &theTheme }

var classesDone bool

func registerClasses() {
	if classesDone {
		return
	}
	classesDone = true
	purego.RegisterFunc(&runLoopRunInMode, cfRunLoopRunInMode)
	registerAppDelegate()
	registerSceneDelegate()
	registerSurfaceClass()
	registerTextViewClass()
}

func registerAppDelegate() {
	_, err := objc.RegisterClass("MyGoAppDelegate", objc.GetClass("UIResponder"),
		[]*objc.Protocol{objc.GetProtocol("UIApplicationDelegate")}, nil,
		[]objc.MethodDef{
			{Cmd: sel("application:didFinishLaunchingWithOptions:"), Fn: func(self id, _ objc.SEL, app, opts id) bool {
				b := theBackend
				b.launched = true
				b.h.Ready()
				return true
			}},
			// Windows live in scenes: from iOS 27 UIKit does not launch an
			// app without them.
			{Cmd: sel("application:configurationForConnectingSceneSession:options:"), Fn: func(self id, _ objc.SEL, app, session, opts id) id {
				c := send(send(class("UISceneConfiguration"), "alloc"), "initWithName:sessionRole:", nsString("Default"), send(session, "role"))
				send(c, "setDelegateClass:", objc.GetClass("MyGoSceneDelegate"))
				return send(c, "autorelease")
			}},
			{Cmd: sel("applicationWillTerminate:"), Fn: func(self id, _ objc.SEL, app id) {
				theBackend.h.Terminating()
			}},
		})
	if err != nil {
		panic(err)
	}
}

// registerSceneDelegate registers the delegate of the window scene, which
// takes the windows made before it connected, and tells the app when it
// becomes active or stops being.
func registerSceneDelegate() {
	_, err := objc.RegisterClass("MyGoSceneDelegate", objc.GetClass("UIResponder"),
		[]*objc.Protocol{objc.GetProtocol("UIWindowSceneDelegate")}, nil,
		[]objc.MethodDef{
			{Cmd: sel("scene:willConnectToSession:options:"), Fn: func(self id, _ objc.SEL, scene, session, opts id) {
				b := theBackend
				b.scene = scene
				for _, w := range b.windows {
					w.attach(scene)
				}
			}},
			{Cmd: sel("sceneDidBecomeActive:"), Fn: func(self id, _ objc.SEL, scene id) {
				theBackend.h.DidBecomeActive()
			}},
			{Cmd: sel("sceneWillResignActive:"), Fn: func(self id, _ objc.SEL, scene id) {
				theBackend.h.DidResignActive()
			}},
		})
	if err != nil {
		panic(err)
	}
}

// mainScreen returns the bounds of the screen in points and how many
// pixels a point is.
func mainScreen() (CGRect, float64) {
	s := send(class("UIScreen"), "mainScreen")
	return msgRect(s, sel("bounds")), msgFloat(s, sel("scale"))
}

type screen struct{}

func (screen) Displays() []platform.Display {
	r, scale := mainScreen()
	b := platform.Rect{Width: int(r.Size.W), Height: int(r.Size.H)}
	return []platform.Display{{ID: 1, Label: "Screen", Bounds: b, WorkArea: b, ScaleFactor: scale, Internal: true, Primary: true}}
}

func (screen) CursorPoint() platform.Point { return platform.Point{} }

type theme struct{ source string }

var theTheme theme

// IsDark reports whether the app shows dark: as the system does, or as
// its source asked.
func (t *theme) IsDark() bool {
	switch t.source {
	case "dark":
		return true
	case "light":
		return false
	}
	tc := send(class("UITraitCollection"), "currentTraitCollection")
	return tc != 0 && sendInt(tc, "userInterfaceStyle") == 2
}

func (t *theme) SetSource(source string)               { t.source = source }
func (t *theme) UIFont() string                        { return "" }
func (t *theme) FontRendering() platform.FontRendering { return platform.FontRendering{} }
func (t *theme) Preferences() platform.Preferences     { return platform.Preferences{} }
