//go:build ios

package ios

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type id = objc.ID

// cgPoint, cgSize and cgRect mirror Core Graphics' structs (64-bit).
type cgPoint struct{ X, Y float64 }
type cgSize struct{ W, H float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

// uiEdgeInsets mirrors UIEdgeInsets.
type uiEdgeInsets struct{ Top, Left, Bottom, Right float64 }

var (
	loaded  bool
	errLoad error

	// Typed variants of objc_msgSend, for structs and floats: Send's
	// generic form registers a function on every call.
	msgRect     func(obj id, sel objc.SEL) cgRect
	msgInitRect func(obj id, sel objc.SEL, r cgRect) id
	msgPointIn  func(obj id, sel objc.SEL, view id) cgPoint
	msgFloat    func(obj id, sel objc.SEL) float64
	msgInsets   func(obj id, sel objc.SEL) uiEdgeInsets
	msgSetFloat func(obj id, sel objc.SEL, v float64)

	uiApplicationMain     uintptr
	pthreadMainNP         uintptr
	cfRunLoopGetMain      uintptr
	cfRunLoopSourceCreate uintptr
	cfRunLoopAddSource    uintptr
	cfRunLoopSourceSignal uintptr
	cfRunLoopWakeUp       uintptr
	cfRunLoopRunInMode    uintptr
	kCFRunLoopCommonModes uintptr
	kCFRunLoopDefaultMode uintptr
	poolPush, poolPop     uintptr
)

func load() error {
	if loaded {
		return errLoad
	}
	loaded = true
	lib := func(path string) uintptr {
		h, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil && errLoad == nil {
			errLoad = fmt.Errorf("ios: cannot load %s: %w", path, err)
		}
		return h
	}
	objcLib := lib("/usr/lib/libobjc.A.dylib")
	system := lib("/usr/lib/libSystem.B.dylib")
	cf := lib("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	uikit := lib("/System/Library/Frameworks/UIKit.framework/UIKit")
	lib("/System/Library/Frameworks/QuartzCore.framework/QuartzCore")
	if errLoad != nil {
		return errLoad
	}
	sym := func(h uintptr, name string) uintptr {
		p, err := purego.Dlsym(h, name)
		if err != nil && errLoad == nil {
			errLoad = fmt.Errorf("ios: %w", err)
		}
		return p
	}
	for _, f := range []any{&msgRect, &msgInitRect, &msgPointIn, &msgFloat, &msgInsets, &msgSetFloat} {
		purego.RegisterLibFunc(f, objcLib, "objc_msgSend")
	}
	uiApplicationMain = sym(uikit, "UIApplicationMain")
	pthreadMainNP = sym(system, "pthread_main_np")
	cfRunLoopGetMain = sym(cf, "CFRunLoopGetMain")
	cfRunLoopSourceCreate = sym(cf, "CFRunLoopSourceCreate")
	cfRunLoopAddSource = sym(cf, "CFRunLoopAddSource")
	cfRunLoopSourceSignal = sym(cf, "CFRunLoopSourceSignal")
	cfRunLoopWakeUp = sym(cf, "CFRunLoopWakeUp")
	cfRunLoopRunInMode = sym(cf, "CFRunLoopRunInMode")
	poolPush = sym(objcLib, "objc_autoreleasePoolPush")
	poolPop = sym(objcLib, "objc_autoreleasePoolPop")
	kCFRunLoopCommonModes = deref(sym(cf, "kCFRunLoopCommonModes"))
	kCFRunLoopDefaultMode = deref(sym(cf, "kCFRunLoopDefaultMode"))
	return errLoad
}

// deref reads the pointer a symbol's address holds, as a CFStringRef
// constant's.
func deref(p uintptr) uintptr {
	if p == 0 {
		return 0
	}
	return **(**uintptr)(unsafe.Pointer(&p))
}

var sels = map[string]objc.SEL{}

func sel(name string) objc.SEL {
	s, ok := sels[name]
	if !ok {
		s = objc.RegisterName(name)
		sels[name] = s
	}
	return s
}

func class(name string) id { return id(objc.GetClass(name)) }

func send(obj id, name string, args ...any) id { return obj.Send(sel(name), args...) }

func sendInt(obj id, name string, args ...any) int {
	return objc.Send[int](obj, sel(name), args...)
}

func sendBool(obj id, name string, args ...any) bool {
	return objc.Send[bool](obj, sel(name), args...)
}

func respondsTo(obj id, name string) bool {
	return obj != 0 && sendBool(obj, "respondsToSelector:", sel(name))
}

// withPool runs fn in an autorelease pool, on this thread.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p, _, _ := purego.SyscallN(poolPush)
	defer purego.SyscallN(poolPop, p)
	fn()
}

// nsString returns an autoreleased NSString of s.
func nsString(s string) id {
	b := append([]byte(s), 0)
	r := send(class("NSString"), "stringWithUTF8String:", uintptr(unsafe.Pointer(&b[0])))
	runtime.KeepAlive(b)
	return r
}

// goString returns the text of an NSString.
func goString(s id) string {
	if s == 0 {
		return ""
	}
	p := objc.Send[*byte](s, sel("UTF8String"))
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
