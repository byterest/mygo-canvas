//go:build windows || pdfium

package pdf

import (
	_ "embed"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// On Windows, PDFium reads the document, its text and its links, and
// renders its pages, as Chrome and Edge do, when its library, pdfium.dll,
// is shipped with the program, as the CLI does from mygo-plugin.json;
// without it, Windows.Data.Pdf renders the pages, and there is no text.
// MYGO_PDFIUM names another file of the library.
//
// Built with the pdfium tag, the engine is there on macOS too, to be
// tested against PDFKit, with the library MYGO_PDFIUM names or
// libpdfium.dylib next to the program; Open still uses PDFKit there.
//
// PDFium is not safe to call from two threads at once, whatever the
// documents: every call takes fz.mu. A page is loaded for one call and
// closed after it, so that what PDFium keeps of a document is little more
// than its bytes.

var fz struct {
	once sync.Once
	ok   bool
	mu   sync.Mutex
	fn   struct {
		initLibrary, loadMemDocument, closeDocument, getPageCount, getPageSizeByIndexF,
		loadPage, closePage, getPageBoundingBox, pageGetRotation,
		bitmapCreateEx, bitmapFillRect, bitmapDestroy, renderPageBitmap,
		textLoadPage, textClosePage, textCountChars, textGetUnicode, textIsGenerated, textGetLooseCharBox,
		linkEnumerate, linkGetAnnotRect, linkGetAction, linkGetDest,
		actionGetType, actionGetURIPath, actionGetDest, destGetDestPageIndex uintptr
	}
}

// fsRect is an FS_RECTF, in PDF's space: up from the bottom left.
type fsRect struct{ Left, Top, Right, Bottom float32 }

const (
	fpdfBitmapBGRA     = 4
	fpdfAnnot          = 0x01
	fpdfReverseBytes   = 0x10 // RGBA in memory, as image.RGBA is
	pdfActionGoto      = 1
	pdfActionURI       = 3
	pdfiumLibraryEnvar = "MYGO_PDFIUM"
)

// plugin names the libraries of PDFium that apps ship (mygo-plugin.json),
// which the CLI downloads into the user's cache.
//
//go:embed mygo-plugin.json
var plugin []byte

// pdfiumPath returns the file of the library to load: the one MYGO_PDFIUM
// names, or the one an app ships, next to the program, or, run with go
// run, in the resources of the project or the CLI's cache.
func pdfiumPath() string {
	if p := os.Getenv(pdfiumLibraryEnvar); p != "" {
		return p
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dirs = append(dirs, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, "resources"))
	}
	var m struct {
		Libraries []struct {
			Files map[string]struct{ Name, SHA256 string }
		}
	}
	if json.Unmarshal(plugin, &m) == nil && len(m.Libraries) > 0 {
		if f, ok := m.Libraries[0].Files[runtime.GOOS+"-"+runtime.GOARCH]; ok && f.SHA256 != "" {
			if cache, err := os.UserCacheDir(); err == nil {
				dirs = append(dirs, filepath.Join(cache, "mygo", "natives", f.SHA256))
			}
		}
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, pdfiumLibraryName)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func loadPDFium() bool {
	fz.once.Do(func() {
		path := pdfiumPath()
		if path == "" {
			return
		}
		lib, err := dlopen(path)
		if err != nil {
			return
		}
		f := &fz.fn
		for _, s := range []struct {
			p    *uintptr
			name string
		}{
			{&f.initLibrary, "FPDF_InitLibrary"},
			{&f.loadMemDocument, "FPDF_LoadMemDocument"},
			{&f.closeDocument, "FPDF_CloseDocument"},
			{&f.getPageCount, "FPDF_GetPageCount"},
			{&f.getPageSizeByIndexF, "FPDF_GetPageSizeByIndexF"},
			{&f.loadPage, "FPDF_LoadPage"},
			{&f.closePage, "FPDF_ClosePage"},
			{&f.getPageBoundingBox, "FPDF_GetPageBoundingBox"},
			{&f.pageGetRotation, "FPDFPage_GetRotation"},
			{&f.bitmapCreateEx, "FPDFBitmap_CreateEx"},
			{&f.bitmapFillRect, "FPDFBitmap_FillRect"},
			{&f.bitmapDestroy, "FPDFBitmap_Destroy"},
			{&f.renderPageBitmap, "FPDF_RenderPageBitmap"},
			{&f.textLoadPage, "FPDFText_LoadPage"},
			{&f.textClosePage, "FPDFText_ClosePage"},
			{&f.textCountChars, "FPDFText_CountChars"},
			{&f.textGetUnicode, "FPDFText_GetUnicode"},
			{&f.textIsGenerated, "FPDFText_IsGenerated"},
			{&f.textGetLooseCharBox, "FPDFText_GetLooseCharBox"},
			{&f.linkEnumerate, "FPDFLink_Enumerate"},
			{&f.linkGetAnnotRect, "FPDFLink_GetAnnotRect"},
			{&f.linkGetAction, "FPDFLink_GetAction"},
			{&f.linkGetDest, "FPDFLink_GetDest"},
			{&f.actionGetType, "FPDFAction_GetType"},
			{&f.actionGetURIPath, "FPDFAction_GetURIPath"},
			{&f.actionGetDest, "FPDFAction_GetDest"},
			{&f.destGetDestPageIndex, "FPDFDest_GetDestPageIndex"},
		} {
			// A library without one of these is too old to use.
			if *s.p, err = dlsym(lib, s.name); err != nil || *s.p == 0 {
				return
			}
		}
		ccall(f.initLibrary)
		fz.ok = true
	})
	return fz.ok
}

// up returns a pointer as an argument of a call.
func up[T any](p *T) uintptr { return uintptr(unsafe.Pointer(p)) }

// cint returns the int a C function returned, which takes the low 32 bits.
func cint(r uintptr) int { return int(int32(r)) }

type fzDoc struct {
	doc   uintptr // FPDF_DOCUMENT
	data  []byte  // the bytes it reads, which must stay until it is closed
	sizes []Size
}

// openPDFium opens a document with PDFium, or returns ErrUnsupported
// without its library.
func openPDFium(data []byte) (engine, error) {
	if !loadPDFium() {
		return nil, ErrUnsupported
	}
	fz.mu.Lock()
	defer fz.mu.Unlock()
	d := &fzDoc{data: append([]byte(nil), data...)}
	d.doc = ccall(fz.fn.loadMemDocument, up(&d.data[0]), uintptr(len(d.data)), 0)
	if d.doc == 0 {
		return nil, ErrInvalid // not a PDF, or one with a password
	}
	n := cint(ccall(fz.fn.getPageCount, d.doc))
	d.sizes = make([]Size, max(n, 0))
	for i := range d.sizes {
		// The size of the page as it shows, its rotation applied, without
		// loading the page.
		var s struct{ W, H float32 }
		if cint(ccall(fz.fn.getPageSizeByIndexF, d.doc, uintptr(i), up(&s))) == 0 {
			ccall(fz.fn.closeDocument, d.doc)
			return nil, ErrInvalid
		}
		d.sizes[i] = Size{float64(s.W), float64(s.H)}
	}
	return d, nil
}

func (d *fzDoc) pages() int      { return len(d.sizes) }
func (d *fzDoc) size(i int) Size { return d.sizes[i] }

// withPage loads a page for fn, and closes it after.
func withPage[T any](d *fzDoc, i int, fn func(page uintptr) (T, error)) (T, error) {
	fz.mu.Lock()
	defer fz.mu.Unlock()
	page := ccall(fz.fn.loadPage, d.doc, uintptr(i))
	if page == 0 {
		var zero T
		return zero, ErrPage
	}
	defer ccall(fz.fn.closePage, page)
	return fn(page)
}

// box returns the box of a page in PDF's space: the part of its media box
// its crop box takes, and its rotation.
func (d *fzDoc) box(page uintptr) box {
	var r fsRect
	ccall(fz.fn.getPageBoundingBox, page, up(&r))
	x0, x1 := min(r.Left, r.Right), max(r.Left, r.Right)
	y0, y1 := min(r.Bottom, r.Top), max(r.Bottom, r.Top)
	rot := cint(ccall(fz.fn.pageGetRotation, page))
	return newBox(float64(x0), float64(y0), float64(x1-x0), float64(y1-y0), rot*90)
}

func (d *fzDoc) render(i int, scale float64, clip image.Rectangle) (*image.RGBA, error) {
	s := d.sizes[i]
	whole := image.Pt(max(int(s.W*scale+0.5), 1), max(int(s.H*scale+0.5), 1))
	return withPage(d, i, func(page uintptr) (*image.RGBA, error) {
		w, h := clip.Dx(), clip.Dy()
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		// PDFium draws in the image's own pixels: there is no copy.
		bmp := ccall(fz.fn.bitmapCreateEx, uintptr(w), uintptr(h), fpdfBitmapBGRA, up(&img.Pix[0]), uintptr(img.Stride))
		if bmp == 0 {
			return nil, ErrInvalid
		}
		defer ccall(fz.fn.bitmapDestroy, bmp)
		ccall(fz.fn.bitmapFillRect, bmp, 0, 0, uintptr(w), uintptr(h), 0xFFFFFFFF)
		// The whole page, turned by its rotation, placed so that the clip
		// is what lands in the bitmap.
		ccall(fz.fn.renderPageBitmap, bmp, page,
			uintptr(-clip.Min.X), uintptr(-clip.Min.Y), uintptr(whole.X), uintptr(whole.Y),
			0, fpdfAnnot|fpdfReverseBytes)
		return img, nil
	})
}

func (d *fzDoc) text(i int) (Text, error) {
	return withPage(d, i, func(page uintptr) (Text, error) {
		tp := ccall(fz.fn.textLoadPage, page)
		if tp == 0 {
			return Text{}, nil
		}
		defer ccall(fz.fn.textClosePage, tp)
		b := d.box(page)
		n := cint(ccall(fz.fn.textCountChars, tp))
		t := Text{Runes: make([]rune, 0, n), Boxes: make([]Rect, 0, n)}
		for j := 0; j < n; j++ {
			r := rune(uint32(ccall(fz.fn.textGetUnicode, tp, uintptr(j))))
			next := func() rune {
				if j+1 < n {
					return rune(uint32(ccall(fz.fn.textGetUnicode, tp, uintptr(j+1))))
				}
				return 0
			}
			var box Rect
			switch {
			case r == '\r' && next() == '\n':
				continue // PDFium ends a line with \r\n; the others with \n
			case r == '\r' || r == '\n':
				r = '\n'
			default:
				if r >= 0xD800 && r < 0xDC00 { // the high half of a pair
					if lo := next(); lo >= 0xDC00 && lo < 0xE000 {
						r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
						box = d.charBox(tp, b, j)
						j++ // the low half, which has no box of its own
						break
					}
				}
				if r == 0 || r > 0x10FFFF || (r >= 0xD800 && r < 0xE000) {
					r = 0xFFFD
				}
				if cint(ccall(fz.fn.textIsGenerated, tp, uintptr(j))) != 1 {
					box = d.charBox(tp, b, j)
				}
			}
			t.Runes = append(t.Runes, r)
			t.Boxes = append(t.Boxes, box)
		}
		return t, nil
	})
}

// charBox returns the box of a character as the page shows, from the top
// of its font to the bottom, as the other engines give it, or an empty one.
func (d *fzDoc) charBox(tp uintptr, b box, j int) Rect {
	var r fsRect
	if cint(ccall(fz.fn.textGetLooseCharBox, tp, uintptr(j), up(&r))) == 0 {
		return Rect{}
	}
	box := b.rect(float64(r.Left), float64(r.Bottom), float64(r.Right), float64(r.Top))
	if box.W <= 0 || box.H <= 0 {
		return Rect{}
	}
	return box
}

func (d *fzDoc) links(i int) ([]Link, error) {
	return withPage(d, i, func(page uintptr) ([]Link, error) {
		b := d.box(page)
		var links []Link
		var pos int32
		var l uintptr
		for cint(ccall(fz.fn.linkEnumerate, page, up(&pos), up(&l))) != 0 {
			link := Link{Page: -1}
			dest := uintptr(0)
			if a := ccall(fz.fn.linkGetAction, l); a != 0 {
				switch cint(ccall(fz.fn.actionGetType, a)) {
				case pdfActionURI:
					link.URL = d.uri(a)
				case pdfActionGoto:
					dest = ccall(fz.fn.actionGetDest, d.doc, a)
				}
			} else {
				dest = ccall(fz.fn.linkGetDest, d.doc, l)
			}
			if dest != 0 {
				link.Page = cint(ccall(fz.fn.destGetDestPageIndex, d.doc, dest))
			}
			if link.URL == "" && link.Page < 0 {
				continue
			}
			var r fsRect
			if cint(ccall(fz.fn.linkGetAnnotRect, l, up(&r))) == 0 {
				continue
			}
			link.Rect = b.rect(float64(r.Left), float64(r.Bottom), float64(r.Right), float64(r.Top))
			links = append(links, link)
		}
		return links, nil
	})
}

// uri returns the address of a URI action, which PDFium gives as bytes
// ending in a 0.
func (d *fzDoc) uri(a uintptr) string {
	n := uint32(ccall(fz.fn.actionGetURIPath, d.doc, a, 0, 0))
	if n <= 1 || n > 1<<16 {
		return ""
	}
	buf := make([]byte, n)
	ccall(fz.fn.actionGetURIPath, d.doc, a, up(&buf[0]), uintptr(n))
	for len(buf) > 0 && buf[len(buf)-1] == 0 {
		buf = buf[:len(buf)-1]
	}
	return string(buf)
}

func (d *fzDoc) close() {
	fz.mu.Lock()
	defer fz.mu.Unlock()
	if d.doc != 0 {
		ccall(fz.fn.closeDocument, d.doc)
		d.doc, d.data = 0, nil
	}
}
