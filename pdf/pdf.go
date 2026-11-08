// Package pdf reads PDF documents with the system's own PDF engine: PDFKit
// and Core Graphics on macOS, Poppler on Linux, and PDFium, or
// Windows.Data.Pdf without it, on Windows, as the system's viewers read
// them, loaded at run time as the rest of MyGo's native code is, without
// cgo.
//
// A Document renders its pages to images at any scale, or a part of one,
// for a page shown zoomed in, and gives the text of a page with the box of
// each character, and its links:
//
//	d, err := pdf.Open(data)
//	if err != nil {
//		return err
//	}
//	defer d.Close()
//	size := d.PageSize(0)              // in points, as the page shows
//	img, err := d.Render(0, 2, image.Rectangle{}) // the whole page, 2 pixels a point
//	text, err := d.Text(0)             // its characters and their boxes
//
// Every box and size is in points, a 72nd of an inch, from the top left
// corner of the page as it shows, its rotation applied. The methods of a
// Document may be called from any goroutine; it runs one at a time.
//
// On Windows, PDFium reads the document when pdfium.dll is next to the
// program; without it, Windows has no text of a page to give: Text returns
// ErrNoText there, and Links none. Where the system has no PDF engine (in a browser, or on Linux
// without Poppler's GLib library, libpoppler-glib), Open returns
// ErrUnsupported.
package pdf

import (
	"errors"
	"image"
	"sync"
)

// Errors.
var (
	// ErrUnsupported is returned by Open where the system has no PDF engine.
	ErrUnsupported = errors.New("pdf: the system has no PDF engine")
	// ErrInvalid is returned by Open for data that is not a PDF the
	// engine reads, or one locked with a password.
	ErrInvalid = errors.New("pdf: not a PDF document")
	// ErrNoText is returned by Text where the engine gives no text.
	ErrNoText = errors.New("pdf: the system's engine gives no text")
	// ErrClosed is returned by the methods of a closed Document.
	ErrClosed = errors.New("pdf: document closed")
	// ErrPage is returned for a page the document does not have.
	ErrPage = errors.New("pdf: no such page")
)

// Size is the size of a page in points.
type Size struct{ W, H float64 }

// Rect is a box on a page, in points from its top left corner.
type Rect struct{ X, Y, W, H float64 }

// Text is the text of a page: its characters, in the order they are read,
// and the box of each, Boxes[i] that of Runes[i]. A character that shows
// nothing, as a space or a newline the engine puts between lines, has an
// empty box.
type Text struct {
	Runes []rune
	Boxes []Rect
}

// String returns the text.
func (t Text) String() string { return string(t.Runes) }

// Link is a link of a page: the box it takes, and the web address it goes
// to, or the page of the document, from 0, when URL is empty.
type Link struct {
	Rect Rect
	URL  string
	Page int
}

// Document is an open PDF document.
type Document struct {
	mu     sync.Mutex
	e      engine
	sizes  []Size
	closed bool
}

// openEngine opens a document with the system's engine; tests change it.
var openEngine = open

// engine is what a system's PDF engine does with a document.
type engine interface {
	pages() int
	size(i int) Size
	render(i int, scale float64, clip image.Rectangle) (*image.RGBA, error)
	text(i int) (Text, error)
	links(i int) ([]Link, error)
	close()
}

// Open opens a PDF document from its bytes, which it copies.
func Open(data []byte) (*Document, error) {
	if len(data) < 5 || string(data[:5]) != "%PDF-" {
		return nil, ErrInvalid
	}
	e, err := openEngine(data)
	if err != nil {
		return nil, err
	}
	d := &Document{e: e}
	n := e.pages()
	if n <= 0 {
		e.close()
		return nil, ErrInvalid
	}
	d.sizes = make([]Size, n)
	for i := range d.sizes {
		d.sizes[i] = e.size(i)
	}
	return d, nil
}

// Pages returns how many pages the document has.
func (d *Document) Pages() int { return len(d.sizes) }

// PageSize returns the size of a page as it shows, in points: the size of
// its crop box, turned by its rotation. It is zero for a page the document
// does not have.
func (d *Document) PageSize(i int) Size {
	if i < 0 || i >= len(d.sizes) {
		return Size{}
	}
	return d.sizes[i]
}

// Render renders a page on white, scale pixels a point: the whole of it,
// or the part clip takes of the whole, in the pixels of the whole, as a
// page zoomed far in needs. The image's bounds start at 0, 0 whatever the
// clip.
func (d *Document) Render(i int, scale float64, clip image.Rectangle) (*image.RGBA, error) {
	if !(scale > 0) {
		return nil, errors.New("pdf: render at a scale of 0")
	}
	s := d.PageSize(i)
	whole := image.Rect(0, 0, max(int(s.W*scale+0.5), 1), max(int(s.H*scale+0.5), 1))
	if clip.Empty() {
		clip = whole
	}
	clip = clip.Intersect(whole)
	if clip.Empty() {
		return nil, errors.New("pdf: render outside the page")
	}
	return lock(d, i, func() (*image.RGBA, error) { return d.e.render(i, scale, clip) })
}

// Text returns the text of a page, with the box of each character.
func (d *Document) Text(i int) (Text, error) {
	return lock(d, i, func() (Text, error) { return d.e.text(i) })
}

// Links returns the links of a page.
func (d *Document) Links(i int) ([]Link, error) {
	return lock(d, i, func() ([]Link, error) { return d.e.links(i) })
}

// Close lets go of the document. It is safe to call more than once.
func (d *Document) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		d.e.close()
	}
}

// lock runs fn on a page of an open document, one call at a time.
func lock[T any](d *Document, i int, fn func() (T, error)) (T, error) {
	var zero T
	if i < 0 || i >= len(d.sizes) {
		return zero, ErrPage
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return zero, ErrClosed
	}
	return fn()
}

// box is a page's box in the engine's space, which is a PDF's: from its
// bottom left corner, up, before the page is turned by its rotation.
type box struct {
	x, y, w, h float64
	rotate     int // clockwise, 0, 90, 180 or 270
}

// newBox returns a box, its rotation made one of 0, 90, 180 and 270.
func newBox(x, y, w, h float64, rotate int) box {
	return box{x, y, w, h, ((rotate%360 + 360) % 360) / 90 * 90}
}

// size returns the size of the page as it shows.
func (b box) size() Size {
	if b.rotate == 90 || b.rotate == 270 {
		return Size{b.h, b.w}
	}
	return Size{b.w, b.h}
}

// point returns a point of the PDF's space as it shows on the page: from
// its top left corner, down, turned by its rotation.
func (b box) point(x, y float64) (float64, float64) {
	x, y = x-b.x, y-b.y
	switch b.rotate {
	case 90:
		return y, x
	case 180:
		return b.w - x, y
	case 270:
		return b.h - y, b.w - x
	}
	return x, b.h - y
}

// rect returns a rectangle of the PDF's space as it shows on the page.
func (b box) rect(x0, y0, x1, y1 float64) Rect {
	ax, ay := b.point(x0, y0)
	bx, by := b.point(x1, y1)
	return Rect{min(ax, bx), min(ay, by), abs(bx - ax), abs(by - ay)}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
