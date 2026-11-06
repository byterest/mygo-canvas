package pdf

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// On Windows, Windows.Data.Pdf reads the document and renders its pages,
// as Edge's reader of old and the Photos app do. It gives no text and no
// links: Text returns ErrNoText, and Links none.
//
// The Windows Runtime's objects are used on a thread of their own, which
// joins the multithreaded apartment, so that their asynchronous calls
// complete without a message loop: every call of an engine runs there.

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	iidPdfDocumentStatics   = guid{0x433a0b5f, 0xc007, 0x4788, [8]byte{0x90, 0xf2, 0x08, 0x14, 0x3d, 0x92, 0x25, 0x99}}
	iidPdfPageRenderOptions = guid{0x3c98056f, 0xb7cf, 0x4c29, [8]byte{0x9a, 0x04, 0x52, 0xd9, 0x02, 0x67, 0xf4, 0x25}}
	iidAsyncInfo            = guid{0x00000036, 0x0000, 0x0000, [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidRandomAccessStream   = guid{0x905a0fe1, 0xbc53, 0x11df, [8]byte{0x8c, 0x49, 0x00, 0x1e, 0x4f, 0xc6, 0x86, 0xda}}
)

// Vtable slots: IUnknown takes the first three, IInspectable six.
const (
	slotQueryInterface = 0
	slotRelease        = 2

	slotStreamRead = 3
	slotStreamSeek = 5

	slotAsyncStatus = 7

	slotOperationGetResults = 8

	slotLoadFromStreamAsync = 8 // IPdfDocumentStatics

	slotGetPage   = 6 // IPdfDocument
	slotPageCount = 7

	slotRenderWithOptions = 7 // IPdfPage
	slotPageSize          = 10

	slotSetSourceRect        = 7 // IPdfPageRenderOptions
	slotSetDestinationWidth  = 9
	slotSetDestinationHeight = 11
	slotSetBackgroundColor   = 13
)

// dipsPerPoint is how many of the DIPs Windows.Data.Pdf measures pages in
// a point takes: 96 an inch to 72.
const dipsPerPoint = 96.0 / 72

var rt struct {
	once  sync.Once
	ok    bool
	calls chan func()

	roInitialize, roGetActivationFactory, roActivateInstance,
	createString, deleteString, shCreateMemStream, streamOverStream *syscall.LazyProc
}

func load() bool {
	rt.once.Do(func() {
		combase := syscall.NewLazyDLL("combase.dll")
		shlwapi := syscall.NewLazyDLL("shlwapi.dll")
		shcore := syscall.NewLazyDLL("shcore.dll")
		rt.roInitialize = combase.NewProc("RoInitialize")
		rt.roGetActivationFactory = combase.NewProc("RoGetActivationFactory")
		rt.roActivateInstance = combase.NewProc("RoActivateInstance")
		rt.createString = combase.NewProc("WindowsCreateString")
		rt.deleteString = combase.NewProc("WindowsDeleteString")
		rt.shCreateMemStream = shlwapi.NewProc("SHCreateMemStream")
		rt.streamOverStream = shcore.NewProc("CreateRandomAccessStreamOverStream")
		for _, p := range []*syscall.LazyProc{rt.roInitialize, rt.roGetActivationFactory, rt.roActivateInstance,
			rt.createString, rt.deleteString, rt.shCreateMemStream, rt.streamOverStream} {
			if p.Find() != nil {
				return
			}
		}
		rt.calls = make(chan func())
		ready := make(chan bool)
		go func() {
			runtime.LockOSThread()
			const roInitMultithreaded = 1
			hr, _, _ := rt.roInitialize.Call(roInitMultithreaded)
			// S_FALSE: initialized already, which is as good.
			ready <- int32(hr) >= 0
			for fn := range rt.calls {
				fn()
			}
		}()
		rt.ok = <-ready
	})
	return rt.ok
}

// on runs fn on the thread of the Windows Runtime.
func on[T any](fn func() (T, error)) (T, error) {
	var v T
	var err error
	done := make(chan struct{})
	rt.calls <- func() {
		v, err = fn()
		close(done)
	}
	<-done
	return v, err
}

// call calls a method of a COM object by its vtable slot.
func call(obj uintptr, slot int, args ...uintptr) int32 {
	vtbl := *(*uintptr)(ptr(obj))
	fn := *(*uintptr)(ptr(vtbl + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	hr, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return int32(hr)
}

func release(obj uintptr) {
	if obj != 0 {
		call(obj, slotRelease)
	}
}

// ptr returns the pointer an address of COM's memory is, which is not Go's.
func ptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

var errHR = errors.New("pdf: Windows.Data.Pdf failed")

// hstring makes an HSTRING, to delete.
func hstring(s string) (uintptr, error) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h uintptr
	if hr, _, _ := rt.createString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h))); int32(hr) < 0 {
		return 0, errHR
	}
	return h, nil
}

// await waits for an asynchronous operation or action to end, and reports
// whether it completed.
func await(op uintptr) bool {
	var info uintptr
	if call(op, slotQueryInterface, uintptr(unsafe.Pointer(&iidAsyncInfo)), uintptr(unsafe.Pointer(&info))) < 0 {
		return false
	}
	defer release(info)
	const started, completed = 0, 1
	for wait := time.Microsecond * 100; ; wait = min(wait*2, 5*time.Millisecond) {
		var status int32
		if call(info, slotAsyncStatus, uintptr(unsafe.Pointer(&status))) < 0 {
			return false
		}
		if status != started {
			return status == completed
		}
		time.Sleep(wait)
	}
}

// memStream makes an IRandomAccessStream over a copy of data, or an empty
// one, and returns the IStream under it too.
func memStream(data []byte) (stream, ras uintptr, err error) {
	var p uintptr
	if len(data) > 0 {
		p = uintptr(unsafe.Pointer(&data[0]))
	}
	stream, _, _ = rt.shCreateMemStream.Call(p, uintptr(len(data)))
	if stream == 0 {
		return 0, 0, errHR
	}
	const bsosDefault = 0
	if hr, _, _ := rt.streamOverStream.Call(stream, bsosDefault, uintptr(unsafe.Pointer(&iidRandomAccessStream)), uintptr(unsafe.Pointer(&ras))); int32(hr) < 0 {
		release(stream)
		return 0, 0, errHR
	}
	return stream, ras, nil
}

type winDoc struct {
	doc   uintptr // IPdfDocument
	sizes []Size
}

func open(data []byte) (engine, error) {
	if !load() {
		return nil, ErrUnsupported
	}
	return on(func() (engine, error) {
		stream, ras, err := memStream(data)
		if err != nil {
			return nil, err
		}
		defer release(stream)
		defer release(ras)
		name, err := hstring("Windows.Data.Pdf.PdfDocument")
		if err != nil {
			return nil, err
		}
		defer rt.deleteString.Call(name)
		var statics uintptr
		if hr, _, _ := rt.roGetActivationFactory.Call(name, uintptr(unsafe.Pointer(&iidPdfDocumentStatics)), uintptr(unsafe.Pointer(&statics))); int32(hr) < 0 {
			return nil, ErrUnsupported
		}
		defer release(statics)
		var op uintptr
		if call(statics, slotLoadFromStreamAsync, ras, uintptr(unsafe.Pointer(&op))) < 0 {
			return nil, ErrInvalid
		}
		defer release(op)
		if !await(op) {
			return nil, ErrInvalid // not a PDF, or one with a password
		}
		d := &winDoc{}
		if call(op, slotOperationGetResults, uintptr(unsafe.Pointer(&d.doc))) < 0 || d.doc == 0 {
			return nil, ErrInvalid
		}
		var n uint32
		call(d.doc, slotPageCount, uintptr(unsafe.Pointer(&n)))
		d.sizes = make([]Size, n)
		for i := range d.sizes {
			p, err := d.page(i)
			if err != nil {
				release(d.doc)
				return nil, err
			}
			var s struct{ W, H float32 }
			call(p, slotPageSize, uintptr(unsafe.Pointer(&s)))
			release(p)
			d.sizes[i] = Size{float64(s.W) / dipsPerPoint, float64(s.H) / dipsPerPoint}
		}
		return d, nil
	})
}

func (d *winDoc) page(i int) (uintptr, error) {
	var p uintptr
	if call(d.doc, slotGetPage, uintptr(i), uintptr(unsafe.Pointer(&p))) < 0 || p == 0 {
		return 0, ErrPage
	}
	return p, nil
}

func (d *winDoc) pages() int      { return len(d.sizes) }
func (d *winDoc) size(i int) Size { return d.sizes[i] }

func (d *winDoc) render(i int, scale float64, clip image.Rectangle) (*image.RGBA, error) {
	return on(func() (*image.RGBA, error) {
		// A part of a page is a source rectangle, a Rect of four floats,
		// which x64 passes by reference and arm64 in floating-point
		// registers, which a system call cannot: there the whole page is
		// rendered, and the part cut from it.
		whole := image.Rect(0, 0, max(int(d.sizes[i].W*scale+0.5), 1), max(int(d.sizes[i].H*scale+0.5), 1))
		part := clip
		if runtime.GOARCH != "amd64" {
			part = whole
		}
		img, err := d.renderPart(i, scale, part, part != whole)
		if err != nil || part == clip {
			return img, err
		}
		out := image.NewRGBA(image.Rect(0, 0, clip.Dx(), clip.Dy()))
		draw.Draw(out, out.Bounds(), img, clip.Min, draw.Src)
		return out, nil
	})
}

func (d *winDoc) renderPart(i int, scale float64, clip image.Rectangle, cut bool) (*image.RGBA, error) {
	p, err := d.page(i)
	if err != nil {
		return nil, err
	}
	defer release(p)
	name, err := hstring("Windows.Data.Pdf.PdfPageRenderOptions")
	if err != nil {
		return nil, err
	}
	defer rt.deleteString.Call(name)
	var inspectable, opts uintptr
	if hr, _, _ := rt.roActivateInstance.Call(name, uintptr(unsafe.Pointer(&inspectable))); int32(hr) < 0 {
		return nil, errHR
	}
	defer release(inspectable)
	if call(inspectable, slotQueryInterface, uintptr(unsafe.Pointer(&iidPdfPageRenderOptions)), uintptr(unsafe.Pointer(&opts))) < 0 {
		return nil, errHR
	}
	defer release(opts)
	if cut {
		// In the DIPs of the page.
		k := float32(dipsPerPoint / scale)
		src := struct{ X, Y, W, H float32 }{float32(clip.Min.X) * k, float32(clip.Min.Y) * k, float32(clip.Dx()) * k, float32(clip.Dy()) * k}
		call(opts, slotSetSourceRect, uintptr(unsafe.Pointer(&src)))
	}
	call(opts, slotSetDestinationWidth, uintptr(clip.Dx()))
	call(opts, slotSetDestinationHeight, uintptr(clip.Dy()))
	call(opts, slotSetBackgroundColor, 0xffffffff) // Windows.UI.Color, ARGB a byte each: white
	stream, ras, err := memStream(nil)
	if err != nil {
		return nil, err
	}
	defer release(stream)
	defer release(ras)
	var action uintptr
	if call(p, slotRenderWithOptions, ras, opts, uintptr(unsafe.Pointer(&action))) < 0 {
		return nil, errHR
	}
	defer release(action)
	if !await(action) {
		return nil, errHR
	}
	// What was rendered is a PNG, in the stream from its start.
	const seekSet, seekEnd = 0, 2
	var size uint64
	call(stream, slotStreamSeek, 0, seekEnd, uintptr(unsafe.Pointer(&size)))
	call(stream, slotStreamSeek, 0, seekSet, 0)
	buf := make([]byte, size)
	var read uint32
	if len(buf) == 0 || call(stream, slotStreamRead, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&read))) < 0 {
		return nil, errHR
	}
	m, err := png.Decode(bytes.NewReader(buf[:read]))
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, clip.Dx(), clip.Dy()))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(img, img.Bounds(), m, m.Bounds().Min, draw.Over)
	return img, nil
}

func (d *winDoc) text(int) (Text, error)    { return Text{}, ErrNoText }
func (d *winDoc) links(int) ([]Link, error) { return nil, nil }

func (d *winDoc) close() {
	on(func() (struct{}, error) {
		release(d.doc)
		return struct{}{}, nil
	})
}
