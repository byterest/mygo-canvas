package pdf

import (
	"image"
	"runtime"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// On macOS, PDFKit reads the document, its text and its links, and draws
// its pages into a Core Graphics bitmap: what Preview does.

type cgPoint struct{ X, Y float64 }
type cgSize struct{ W, H float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

var kit struct {
	once sync.Once
	ok   bool

	colorSpaceCreateDeviceRGB func() uintptr
	colorSpaceRelease         func(uintptr)
	bitmapContextCreate       func(data unsafe.Pointer, w, h, bpc, rowBytes uintptr, cs uintptr, info uint32) uintptr
	contextRelease            func(uintptr)
	setRGBFillColor           func(ctx uintptr, r, g, b, a float64)
	fillRect                  func(ctx uintptr, r cgRect)
	translateCTM              func(ctx uintptr, x, y float64)
	scaleCTM                  func(ctx uintptr, x, y float64)
	setInterpolationQuality   func(ctx uintptr, q int32)

	poolPush func() uintptr
	poolPop  func(uintptr)

	pdfDocument, nsData objc.Class

	alloc, initWithData, dataWithBytes, release, pageCount, pageAtIndex, boundsForBox, rotation,
	drawWithBox, string_, utf8String, length, characterBounds, annotations, count, objectAtIndex,
	bounds, url, absoluteString, destination, page, indexForPage, action, respondsTo, isLocked sel
}

type sel = objc.SEL

// kPDFDisplayBoxCropBox is the box pages show: their crop box.
const kPDFDisplayBoxCropBox = 1

func load() bool {
	kit.once.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		if _, err := purego.Dlopen("/System/Library/Frameworks/PDFKit.framework/PDFKit", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
			return
		}
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&kit.colorSpaceCreateDeviceRGB, cg, "CGColorSpaceCreateDeviceRGB")
		purego.RegisterLibFunc(&kit.colorSpaceRelease, cg, "CGColorSpaceRelease")
		purego.RegisterLibFunc(&kit.bitmapContextCreate, cg, "CGBitmapContextCreate")
		purego.RegisterLibFunc(&kit.contextRelease, cg, "CGContextRelease")
		purego.RegisterLibFunc(&kit.setRGBFillColor, cg, "CGContextSetRGBFillColor")
		purego.RegisterLibFunc(&kit.fillRect, cg, "CGContextFillRect")
		purego.RegisterLibFunc(&kit.translateCTM, cg, "CGContextTranslateCTM")
		purego.RegisterLibFunc(&kit.scaleCTM, cg, "CGContextScaleCTM")
		purego.RegisterLibFunc(&kit.setInterpolationQuality, cg, "CGContextSetInterpolationQuality")
		purego.RegisterLibFunc(&kit.poolPush, lib, "objc_autoreleasePoolPush")
		purego.RegisterLibFunc(&kit.poolPop, lib, "objc_autoreleasePoolPop")
		kit.pdfDocument = objc.GetClass("PDFDocument")
		kit.nsData = objc.GetClass("NSData")
		if kit.pdfDocument == 0 || kit.nsData == 0 {
			return
		}
		for p, name := range map[*sel]string{
			&kit.alloc: "alloc", &kit.initWithData: "initWithData:", &kit.dataWithBytes: "dataWithBytes:length:",
			&kit.release: "release", &kit.pageCount: "pageCount", &kit.pageAtIndex: "pageAtIndex:",
			&kit.boundsForBox: "boundsForBox:", &kit.rotation: "rotation", &kit.drawWithBox: "drawWithBox:toContext:",
			&kit.string_: "string", &kit.utf8String: "UTF8String", &kit.length: "length",
			&kit.characterBounds: "characterBoundsAtIndex:", &kit.annotations: "annotations", &kit.count: "count",
			&kit.objectAtIndex: "objectAtIndex:", &kit.bounds: "bounds", &kit.url: "URL",
			&kit.absoluteString: "absoluteString", &kit.destination: "destination", &kit.page: "page",
			&kit.indexForPage: "indexForPage:", &kit.action: "action", &kit.respondsTo: "respondsToSelector:",
			&kit.isLocked: "isLocked",
		} {
			*p = objc.RegisterName(name)
		}
		kit.ok = true
	})
	return kit.ok
}

// pool runs fn in an autorelease pool: the methods run on any goroutine's
// thread, which has none of its own.
func pool[T any](fn func() T) T {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p := kit.poolPush()
	defer kit.poolPop(p)
	return fn()
}

type kitDoc struct {
	doc   objc.ID
	boxes []box
}

func open(data []byte) (engine, error) {
	if !load() {
		return nil, ErrUnsupported
	}
	return pool(func() engineResult {
		bytes := objc.ID(kit.nsData).Send(kit.dataWithBytes, unsafe.Pointer(&data[0]), len(data))
		doc := objc.ID(kit.pdfDocument).Send(kit.alloc).Send(kit.initWithData, bytes)
		if doc == 0 {
			return engineResult{err: ErrInvalid}
		}
		if objc.Send[bool](doc, kit.isLocked) {
			doc.Send(kit.release)
			return engineResult{err: ErrInvalid}
		}
		d := &kitDoc{doc: doc}
		n := objc.Send[int](doc, kit.pageCount)
		d.boxes = make([]box, n)
		for i := range n {
			p := doc.Send(kit.pageAtIndex, i)
			r := objc.Send[cgRect](p, kit.boundsForBox, kPDFDisplayBoxCropBox)
			d.boxes[i] = newBox(r.Origin.X, r.Origin.Y, r.Size.W, r.Size.H, objc.Send[int](p, kit.rotation))
		}
		return engineResult{e: d}
	}).get()
}

type engineResult struct {
	e   engine
	err error
}

func (r engineResult) get() (engine, error) { return r.e, r.err }

func (d *kitDoc) pages() int      { return len(d.boxes) }
func (d *kitDoc) size(i int) Size { return d.boxes[i].size() }

func (d *kitDoc) render(i int, scale float64, clip image.Rectangle) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, clip.Dx(), clip.Dy()))
	var pin runtime.Pinner
	pin.Pin(&img.Pix[0])
	defer pin.Unpin()
	pool(func() struct{} {
		cs := kit.colorSpaceCreateDeviceRGB()
		defer kit.colorSpaceRelease(cs)
		// RGBA in that order, premultiplied, which white under it makes
		// opaque throughout.
		const info = 1 | 4<<12 // kCGImageAlphaPremultipliedLast, kCGBitmapByteOrder32Big
		ctx := kit.bitmapContextCreate(unsafe.Pointer(&img.Pix[0]), uintptr(clip.Dx()), uintptr(clip.Dy()), 8, uintptr(img.Stride), cs, info)
		if ctx == 0 {
			return struct{}{}
		}
		defer kit.contextRelease(ctx)
		kit.setInterpolationQuality(ctx, 4) // kCGInterpolationHigh, for the pictures of a page
		kit.setRGBFillColor(ctx, 1, 1, 1, 1)
		kit.fillRect(ctx, cgRect{Size: cgSize{float64(clip.Dx()), float64(clip.Dy())}})
		// Core Graphics goes up from the bottom left: the clip's bottom
		// is that far up the whole page.
		s := d.boxes[i].size()
		bottom := s.H*scale - float64(clip.Max.Y)
		kit.translateCTM(ctx, -float64(clip.Min.X), -bottom)
		kit.scaleCTM(ctx, scale, scale)
		p := d.doc.Send(kit.pageAtIndex, i)
		p.Send(kit.drawWithBox, kPDFDisplayBoxCropBox, ctx)
		return struct{}{}
	})
	return img, nil
}

func (d *kitDoc) text(i int) (Text, error) {
	return pool(func() Text {
		p := d.doc.Send(kit.pageAtIndex, i)
		s := p.Send(kit.string_)
		if s == 0 {
			return Text{}
		}
		n := objc.Send[int](s, kit.length)
		str := goString(objc.Send[*byte](s, kit.utf8String))
		units := utf16.Encode([]rune(str))
		if len(units) != n {
			n = min(n, len(units))
		}
		var t Text
		b := d.boxes[i]
		for at := 0; at < n; {
			r, w := rune(units[at]), 1
			if utf16.IsSurrogate(r) && at+1 < len(units) {
				r, w = utf16.DecodeRune(r, rune(units[at+1])), 2
			}
			c := objc.Send[cgRect](p, kit.characterBounds, at)
			var box Rect
			if c.Size.W > 0 || c.Size.H > 0 {
				box = b.rect(c.Origin.X, c.Origin.Y, c.Origin.X+c.Size.W, c.Origin.Y+c.Size.H)
			}
			t.Runes = append(t.Runes, r)
			t.Boxes = append(t.Boxes, box)
			at += w
		}
		return t
	}), nil
}

func (d *kitDoc) links(i int) ([]Link, error) {
	return pool(func() []Link {
		p := d.doc.Send(kit.pageAtIndex, i)
		annots := p.Send(kit.annotations)
		if annots == 0 {
			return nil
		}
		b := d.boxes[i]
		var links []Link
		for j := range objc.Send[int](annots, kit.count) {
			a := annots.Send(kit.objectAtIndex, j)
			l := Link{Page: -1}
			if u := a.Send(kit.url); u != 0 {
				l.URL = goString(objc.Send[*byte](u.Send(kit.absoluteString), kit.utf8String))
			} else if dest := a.Send(kit.destination); dest != 0 {
				if pg := dest.Send(kit.page); pg != 0 {
					l.Page = objc.Send[int](d.doc, kit.indexForPage, pg)
				}
			} else if act := a.Send(kit.action); act != 0 {
				if objc.Send[bool](act, kit.respondsTo, kit.url) {
					if u := act.Send(kit.url); u != 0 {
						l.URL = goString(objc.Send[*byte](u.Send(kit.absoluteString), kit.utf8String))
					}
				} else if objc.Send[bool](act, kit.respondsTo, kit.destination) {
					if dest := act.Send(kit.destination); dest != 0 {
						if pg := dest.Send(kit.page); pg != 0 {
							l.Page = objc.Send[int](d.doc, kit.indexForPage, pg)
						}
					}
				}
			}
			if l.URL == "" && l.Page < 0 {
				continue // not a link, or one to elsewhere
			}
			r := objc.Send[cgRect](a, kit.bounds)
			l.Rect = b.rect(r.Origin.X, r.Origin.Y, r.Origin.X+r.Size.W, r.Origin.Y+r.Size.H)
			links = append(links, l)
		}
		return links
	}), nil
}

func (d *kitDoc) close() {
	pool(func() struct{} {
		d.doc.Send(kit.release)
		return struct{}{}
	})
}

// goString copies a C string.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
