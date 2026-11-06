package pdf

import (
	"image"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// On Linux, Poppler's GLib library reads the document, its text and its
// links, and draws its pages with cairo: what Evince and Okular do.

var pop struct {
	once sync.Once
	ok   bool

	bytesNew       func(data unsafe.Pointer, n uintptr) uintptr
	bytesUnref     func(uintptr)
	free           func(uintptr)
	errorFree      func(uintptr)
	objectUnref    func(uintptr)
	listFree       func(uintptr)
	docFromBytes   func(bytes uintptr, password uintptr, err *uintptr) uintptr
	docPages       func(doc uintptr) int32
	docPage        func(doc uintptr, i int32) uintptr
	docFindDest    func(doc uintptr, name *byte) uintptr
	destFree       func(uintptr)
	pageSize       func(page uintptr, w, h *float64)
	pageRender     func(page uintptr, cr uintptr)
	pageText       func(page uintptr) uintptr
	pageTextLayout func(page uintptr, rects *uintptr, n *uint32) int32
	pageLinks      func(page uintptr) uintptr
	pageFreeLinks  func(list uintptr)
	pageCropBox    func(page uintptr, rect *popplerRect)
	surfaceCreate  func(format int32, w, h int32) uintptr
	surfaceDestroy func(uintptr)
	surfaceFlush   func(uintptr)
	surfaceData    func(uintptr) uintptr
	surfaceStride  func(uintptr) int32
	cairoCreate    func(surface uintptr) uintptr
	cairoDestroy   func(uintptr)
	cairoSetRGB    func(cr uintptr, r, g, b float64)
	cairoPaint     func(cr uintptr)
	cairoTranslate func(cr uintptr, x, y float64)
	cairoScale     func(cr uintptr, x, y float64)
}

// popplerRect is a PopplerRectangle.
type popplerRect struct{ X1, Y1, X2, Y2 float64 }

func load() bool {
	pop.once.Do(func() {
		dl := func(names ...string) uintptr {
			for _, n := range names {
				if h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
					return h
				}
			}
			return 0
		}
		glib := dl("libglib-2.0.so.0")
		gobj := dl("libgobject-2.0.so.0")
		poppler := dl("libpoppler-glib.so.8")
		cairo := dl("libcairo.so.2")
		if glib == 0 || gobj == 0 || poppler == 0 || cairo == 0 {
			return
		}
		// A library without one of these is too old to use: the bytes of
		// a document came in Poppler 0.82, of 2019.
		for _, s := range []string{"poppler_document_new_from_bytes", "poppler_page_get_text_layout"} {
			if _, err := purego.Dlsym(poppler, s); err != nil {
				return
			}
		}
		purego.RegisterLibFunc(&pop.bytesNew, glib, "g_bytes_new")
		purego.RegisterLibFunc(&pop.bytesUnref, glib, "g_bytes_unref")
		purego.RegisterLibFunc(&pop.free, glib, "g_free")
		purego.RegisterLibFunc(&pop.errorFree, glib, "g_error_free")
		purego.RegisterLibFunc(&pop.listFree, glib, "g_list_free")
		purego.RegisterLibFunc(&pop.objectUnref, gobj, "g_object_unref")
		purego.RegisterLibFunc(&pop.docFromBytes, poppler, "poppler_document_new_from_bytes")
		purego.RegisterLibFunc(&pop.docPages, poppler, "poppler_document_get_n_pages")
		purego.RegisterLibFunc(&pop.docPage, poppler, "poppler_document_get_page")
		purego.RegisterLibFunc(&pop.docFindDest, poppler, "poppler_document_find_dest")
		purego.RegisterLibFunc(&pop.destFree, poppler, "poppler_dest_free")
		purego.RegisterLibFunc(&pop.pageSize, poppler, "poppler_page_get_size")
		purego.RegisterLibFunc(&pop.pageRender, poppler, "poppler_page_render")
		purego.RegisterLibFunc(&pop.pageText, poppler, "poppler_page_get_text")
		purego.RegisterLibFunc(&pop.pageTextLayout, poppler, "poppler_page_get_text_layout")
		purego.RegisterLibFunc(&pop.pageLinks, poppler, "poppler_page_get_link_mapping")
		purego.RegisterLibFunc(&pop.pageFreeLinks, poppler, "poppler_page_free_link_mapping")
		purego.RegisterLibFunc(&pop.surfaceCreate, cairo, "cairo_image_surface_create")
		purego.RegisterLibFunc(&pop.surfaceDestroy, cairo, "cairo_surface_destroy")
		purego.RegisterLibFunc(&pop.surfaceFlush, cairo, "cairo_surface_flush")
		purego.RegisterLibFunc(&pop.surfaceData, cairo, "cairo_image_surface_get_data")
		purego.RegisterLibFunc(&pop.surfaceStride, cairo, "cairo_image_surface_get_stride")
		purego.RegisterLibFunc(&pop.cairoCreate, cairo, "cairo_create")
		purego.RegisterLibFunc(&pop.cairoDestroy, cairo, "cairo_destroy")
		purego.RegisterLibFunc(&pop.cairoSetRGB, cairo, "cairo_set_source_rgb")
		purego.RegisterLibFunc(&pop.cairoPaint, cairo, "cairo_paint")
		purego.RegisterLibFunc(&pop.cairoTranslate, cairo, "cairo_translate")
		purego.RegisterLibFunc(&pop.cairoScale, cairo, "cairo_scale")
		pop.ok = true
	})
	return pop.ok
}

type popDoc struct {
	doc   uintptr
	pgs   []uintptr // PopplerPage, made as they are first asked for
	boxes []box
}

func open(data []byte) (engine, error) {
	if !load() {
		return nil, ErrUnsupported
	}
	bytes := pop.bytesNew(unsafe.Pointer(&data[0]), uintptr(len(data))) // a copy
	defer pop.bytesUnref(bytes)
	var gerr uintptr
	doc := pop.docFromBytes(bytes, 0, &gerr)
	if gerr != 0 {
		pop.errorFree(gerr)
	}
	if doc == 0 {
		return nil, ErrInvalid
	}
	d := &popDoc{doc: doc}
	n := int(pop.docPages(doc))
	d.pgs = make([]uintptr, n)
	d.boxes = make([]box, n)
	for i := range n {
		p := d.page(i)
		// Poppler turns what it gives by the page's rotation: its size,
		// its rendering, its text and its links are as the page shows.
		var w, h float64
		pop.pageSize(p, &w, &h)
		d.boxes[i] = newBox(0, 0, w, h, 0)
	}
	return d, nil
}

func (d *popDoc) page(i int) uintptr {
	if d.pgs[i] == 0 {
		d.pgs[i] = pop.docPage(d.doc, int32(i))
	}
	return d.pgs[i]
}

func (d *popDoc) pages() int      { return len(d.boxes) }
func (d *popDoc) size(i int) Size { return d.boxes[i].size() }

func (d *popDoc) render(i int, scale float64, clip image.Rectangle) (*image.RGBA, error) {
	const formatARGB32 = 0
	w, h := clip.Dx(), clip.Dy()
	surface := pop.surfaceCreate(formatARGB32, int32(w), int32(h))
	defer pop.surfaceDestroy(surface)
	cr := pop.cairoCreate(surface)
	pop.cairoSetRGB(cr, 1, 1, 1)
	pop.cairoPaint(cr)
	pop.cairoTranslate(cr, -float64(clip.Min.X), -float64(clip.Min.Y))
	pop.cairoScale(cr, scale, scale)
	pop.pageRender(d.page(i), cr) // turned by the page's rotation
	pop.cairoDestroy(cr)
	pop.surfaceFlush(surface)
	stride := int(pop.surfaceStride(surface))
	data := pop.surfaceData(surface)
	if data == 0 {
		return nil, ErrInvalid
	}
	src := unsafe.Slice((*byte)(ptr(data)), stride*h)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Cairo's ARGB32 is a native-endian word, BGRA in memory on the
	// little-endian machines Linux runs on; it is opaque on white.
	for y := range h {
		s, o := src[y*stride:y*stride+4*w], img.Pix[y*img.Stride:y*img.Stride+4*w]
		for x := 0; x < 4*w; x += 4 {
			o[x], o[x+1], o[x+2], o[x+3] = s[x+2], s[x+1], s[x], 255
		}
	}
	return img, nil
}

func (d *popDoc) text(i int) (Text, error) {
	p := d.page(i)
	cs := pop.pageText(p)
	if cs == 0 {
		return Text{}, nil
	}
	str := cString(cs)
	pop.free(cs)
	var rects uintptr
	var n uint32
	ok := pop.pageTextLayout(p, &rects, &n) != 0
	var layout []popplerRect
	if ok && rects != 0 {
		layout = unsafe.Slice((*popplerRect)(ptr(rects)), n)
	}
	t := Text{Runes: []rune(str)}
	t.Boxes = make([]Rect, len(t.Runes))
	// The layout's rectangles are those of the text's characters, one
	// each, from the top left corner of the page as it shows.
	for j := range min(len(t.Runes), len(layout)) {
		r := layout[j]
		if r.X2 > r.X1 && r.Y2 > r.Y1 && t.Runes[j] != '\n' {
			t.Boxes[j] = Rect{r.X1, r.Y1, r.X2 - r.X1, r.Y2 - r.Y1}
		}
	}
	if rects != 0 {
		pop.free(rects)
	}
	return t, nil
}

// The layouts of PopplerLinkMapping, PopplerAction and PopplerDest that
// links reads.
type (
	gList struct {
		data, next, prev uintptr
	}
	linkMapping struct {
		area   popplerRect
		action uintptr
	}
	actionHeader struct {
		kind  int32
		_     int32
		title uintptr
		value uintptr // the URI of a URI action, the PopplerDest of a GOTO_DEST one
	}
	popplerDest struct {
		kind    int32
		pageNum int32
		_       [5]float64 // left, bottom, right, top, zoom
		named   uintptr
	}
)

const (
	actionGotoDest = 2
	actionURI      = 5
	destNamed      = 9
)

func (d *popDoc) links(i int) ([]Link, error) {
	list := pop.pageLinks(d.page(i))
	if list == 0 {
		return nil, nil
	}
	defer pop.pageFreeLinks(list)
	b := d.boxes[i]
	var links []Link
	for l := list; l != 0; {
		node := (*gList)(ptr(l))
		l = node.next
		m := (*linkMapping)(ptr(node.data))
		if m == nil || m.action == 0 {
			continue
		}
		a := (*actionHeader)(ptr(m.action))
		link := Link{Page: -1}
		switch a.kind {
		case actionURI:
			link.URL = cString(a.value)
		case actionGotoDest:
			if a.value == 0 {
				continue
			}
			dest := (*popplerDest)(ptr(a.value))
			page := int(dest.pageNum)
			if dest.kind == destNamed && dest.named != 0 {
				name := cString(dest.named) + "\x00"
				if found := pop.docFindDest(d.doc, unsafe.StringData(name)); found != 0 {
					page = int((*popplerDest)(ptr(found)).pageNum)
					pop.destFree(found)
				}
			}
			link.Page = page - 1 // Poppler counts from 1
		}
		if link.URL == "" && link.Page < 0 {
			continue
		}
		// The area of a link is up from the bottom left of the page as it
		// shows.
		r := m.area
		link.Rect = b.rect(r.X1, r.Y1, r.X2, r.Y2)
		links = append(links, link)
	}
	return links, nil
}

func (d *popDoc) close() {
	for _, p := range d.pgs {
		if p != 0 {
			pop.objectUnref(p)
		}
	}
	pop.objectUnref(d.doc)
}

// ptr returns the pointer an address of C's memory is, which is not Go's
// and which its collector neither moves nor frees.
func ptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// cString copies a C string.
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	ptr := (*byte)(ptr(p))
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(ptr), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(ptr, n))
}
