package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"math"
	"strings"
	"testing"
)

// testPDF writes a PDF of two pages, by hand, as the tests know it:
//
// The first is a US Letter page, 612 by 792 points, with "Hello PDF" in
// Helvetica at 24 points whose baseline starts 72 points from the left and
// 700 up, a black square of 100 by 50 at 72, 600 (142 down from the top),
// a link to https://example.com/ over 72, 500 to 272, 530, and one to the
// second page over 72, 400 to 272, 430.
//
// The second is 300 by 200, turned 90° clockwise, so it shows 200 by 300,
// with a black box of 50 by 30 at 10, 10, which shows at 10, 10, 30 by 50,
// and a link to https://example.com/turned over 20, 100 to 80, 130, which
// shows at 100, 20, 30 by 60, and a T at 24 points on a baseline from
// 150, 50, which shows on its side at about 50, 150, 17 by 15.
func testPDF() []byte {
	objs := []string{
		1: "<< /Type /Catalog /Pages 2 0 R >>",
		2: "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 5 0 R " +
			"/Resources << /Font << /F1 7 0 R >> >> /Annots [8 0 R 9 0 R] >>",
		4: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Rotate 90 /Contents 6 0 R " +
			"/Resources << /Font << /F1 7 0 R >> >> /Annots [10 0 R] >>",
		5: stream("BT /F1 24 Tf 72 700 Td (Hello PDF) Tj ET\n0 0 0 rg 72 600 100 50 re f\n"),
		6: stream("0 0 0 rg 10 10 50 30 re f\nBT /F1 24 Tf 150 50 Td (T) Tj ET\n"),
		7: "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		8: "<< /Type /Annot /Subtype /Link /Rect [72 500 272 530] /Border [0 0 0] " +
			"/A << /S /URI /URI (https://example.com/) >> >>",
		9: "<< /Type /Annot /Subtype /Link /Rect [72 400 272 430] /Border [0 0 0] /Dest [4 0 R /Fit] >>",
		10: "<< /Type /Annot /Subtype /Link /Rect [20 100 80 130] /Border [0 0 0] " +
			"/A << /S /URI /URI (https://example.com/turned) >> >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i := 1; i < len(objs); i++ {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i, objs[i])
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs))
	for i := 1; i < len(objs); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs), xref)
	return b.Bytes()
}

func stream(s string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(s), s)
}

// open opens the test PDF, or skips the test where there is no engine.
func openTest(t *testing.T) *Document {
	t.Helper()
	d, err := Open(testPDF())
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestPages(t *testing.T) {
	d := openTest(t)
	if n := d.Pages(); n != 2 {
		t.Fatalf("%d pages, want 2", n)
	}
	if s := d.PageSize(0); s != (Size{612, 792}) {
		t.Errorf("the first page is %v, want 612x792", s)
	}
	// Turned, the second shows taller than wide.
	if s := d.PageSize(1); s != (Size{200, 300}) {
		t.Errorf("the turned page is %v, want 200x300", s)
	}
	if s := d.PageSize(2); s != (Size{}) {
		t.Errorf("a page past the last is %v, want none", s)
	}
}

func TestOpenRejects(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("hello"), []byte("%PDF-1.4\ngarbage")} {
		if d, err := Open(data); err == nil {
			d.Close()
			t.Errorf("Open(%q) opened", data)
		} else if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrUnsupported) {
			t.Errorf("Open(%q) = %v, want ErrInvalid", data, err)
		}
	}
}

// dark reports whether a pixel is ink.
func dark(img *image.RGBA, x, y int) bool {
	c := img.RGBAAt(x, y)
	return c.R < 80 && c.G < 80 && c.B < 80 && c.A == 255
}

// inkBox returns the box of the ink of an image within r.
func inkBox(img *image.RGBA, r image.Rectangle) image.Rectangle {
	var out image.Rectangle
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if dark(img, x, y) {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

func TestRender(t *testing.T) {
	d := openTest(t)
	img, err := d.Render(0, 2, image.Rectangle{})
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b != image.Rect(0, 0, 1224, 1584) {
		t.Fatalf("rendered at 2, the page is %v, want 1224x1584", b)
	}
	if c := img.RGBAAt(5, 5); c.R != 255 || c.G != 255 || c.B != 255 || c.A != 255 {
		t.Errorf("the corner of the page is %v, want white", c)
	}
	// The square at 72, 142, 100 by 50, at 2 pixels a point.
	sq := inkBox(img, image.Rect(100, 240, 400, 440))
	if want := image.Rect(144, 284, 344, 384); !nearRect(sq, want, 2) {
		t.Errorf("the square shows at %v, want %v", sq, want)
	}
	// The text above it.
	if ink := inkBox(img, image.Rect(100, 100, 600, 200)); ink.Empty() {
		t.Error("the text does not show")
	}
}

func nearRect(a, b image.Rectangle, tol int) bool {
	d := func(x, y int) bool { return x-y <= tol && y-x <= tol }
	return d(a.Min.X, b.Min.X) && d(a.Min.Y, b.Min.Y) && d(a.Max.X, b.Max.X) && d(a.Max.Y, b.Max.Y)
}

// A part of a page renders as that part of the whole does.
func TestRenderClip(t *testing.T) {
	d := openTest(t)
	whole, err := d.Render(0, 3, image.Rectangle{})
	if err != nil {
		t.Fatal(err)
	}
	clip := image.Rect(180, 380, 600, 640) // over the square's corner
	part, err := d.Render(0, 3, clip)
	if err != nil {
		t.Fatal(err)
	}
	if b := part.Bounds(); b != image.Rect(0, 0, clip.Dx(), clip.Dy()) {
		t.Fatalf("the part is %v, want %v", b, clip.Size())
	}
	want := inkBox(whole, clip).Sub(clip.Min)
	if got := inkBox(part, part.Bounds()); !nearRect(got, want, 1) {
		t.Errorf("the square shows at %v of the part, want %v", got, want)
	}
}

// A turned page renders turned.
func TestRenderRotated(t *testing.T) {
	d := openTest(t)
	img, err := d.Render(1, 2, image.Rectangle{})
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b != image.Rect(0, 0, 400, 600) {
		t.Fatalf("the turned page is %v, want 400x600", b)
	}
	if got, want := inkBox(img, image.Rect(0, 0, 96, 200)), image.Rect(20, 20, 80, 120); !nearRect(got, want, 2) {
		t.Errorf("the box shows at %v, want %v", got, want)
	}
}

func TestText(t *testing.T) {
	d := openTest(t)
	text, err := d.Text(0)
	if errors.Is(err, ErrNoText) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	s := text.String()
	i := strings.Index(s, "Hello PDF")
	if i < 0 {
		t.Fatalf("the text is %q, want Hello PDF in it", s)
	}
	if len(text.Boxes) != len(text.Runes) {
		t.Fatalf("%d boxes for %d characters", len(text.Boxes), len(text.Runes))
	}
	i = len([]rune(s[:i]))
	h := text.Boxes[i]
	// H at 72 from the left, on a baseline 92 down: its box takes some of
	// the 24 points above it.
	if !near(h.X, 72, 2) || h.Y < 60 || h.Y > 90 || h.Y+h.H < 88 || h.Y+h.H > 100 || h.W < 10 || h.W > 22 {
		t.Errorf("H is at %+v, want about 72, 70, 17 by 22", h)
	}
	if f := text.Boxes[i+8]; f.X <= h.X+50 {
		t.Errorf("F is at %+v, not to the right of H at %+v", f, h)
	}
}

func TestLinks(t *testing.T) {
	d := openTest(t)
	links, err := d.Links(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		if _, err := d.Text(0); errors.Is(err, ErrNoText) {
			t.Skip("the engine gives no links")
		}
	}
	var web, page *Link
	for i := range links {
		if links[i].URL != "" {
			web = &links[i]
		} else {
			page = &links[i]
		}
	}
	if web == nil || web.URL != "https://example.com/" {
		t.Fatalf("the links are %+v, want one to https://example.com/", links)
	}
	if r := web.Rect; !near(r.X, 72, 1) || !near(r.Y, 262, 1) || !near(r.W, 200, 1) || !near(r.H, 30, 1) {
		t.Errorf("the web link takes %+v, want 72, 262, 200 by 30", r)
	}
	if page == nil || page.Page != 1 {
		t.Fatalf("the links are %+v, want one to the second page", links)
	}
	if r := page.Rect; !near(r.Y, 362, 1) || !near(r.H, 30, 1) {
		t.Errorf("the page link takes %+v, want 72, 362, 200 by 30", r)
	}
}

// The text of a turned page is where it shows.
func TestTextRotated(t *testing.T) {
	d := openTest(t)
	text, err := d.Text(1)
	if errors.Is(err, ErrNoText) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	i := strings.IndexRune(text.String(), 'T')
	if i < 0 {
		t.Fatalf("the text is %q, want a T", text.String())
	}
	b := text.Boxes[len([]rune(text.String()[:i]))]
	if b.X < 40 || b.X > 56 || b.X+b.W < 64 || b.X+b.W > 76 || !near(b.Y, 150, 2) || b.H < 10 || b.H > 18 {
		t.Errorf("the T is at %+v, want about 50, 150, 17 by 15", b)
	}
}

// The links of a turned page are where they show.
func TestLinksRotated(t *testing.T) {
	d := openTest(t)
	links, err := d.Links(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		if _, err := d.Text(0); errors.Is(err, ErrNoText) {
			t.Skip("the engine gives no links")
		}
	}
	if len(links) != 1 || links[0].URL != "https://example.com/turned" {
		t.Fatalf("the links are %+v, want one to https://example.com/turned", links)
	}
	if r := links[0].Rect; !near(r.X, 100, 1) || !near(r.Y, 20, 1) || !near(r.W, 30, 1) || !near(r.H, 60, 1) {
		t.Errorf("the link takes %+v, want 100, 20, 30 by 60", r)
	}
}

func TestClosed(t *testing.T) {
	d := openTest(t)
	d.Close()
	d.Close()
	if _, err := d.Render(0, 1, image.Rectangle{}); !errors.Is(err, ErrClosed) {
		t.Errorf("rendering a closed document: %v, want ErrClosed", err)
	}
}

// Pages render at once from several goroutines, one at a time.
func TestConcurrent(t *testing.T) {
	d := openTest(t)
	done := make(chan error)
	for i := range 8 {
		go func() {
			_, err := d.Render(i%2, 1, image.Rectangle{})
			done <- err
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestBox(t *testing.T) {
	// The test PDF's second page, turned 90°: its box at 10, 10, 50 by 30
	// shows at 10, 10, 30 by 50.
	b := newBox(0, 0, 300, 200, 90)
	if r := b.rect(10, 10, 60, 40); r != (Rect{10, 10, 30, 50}) {
		t.Errorf("turned 90°, the box shows at %+v", r)
	}
	for _, c := range []struct {
		rot  int
		want Rect
	}{
		{0, Rect{10, 160, 50, 30}},
		{180, Rect{240, 10, 50, 30}},
		{270, Rect{160, 240, 30, 50}},
		{-90, Rect{160, 240, 30, 50}},
	} {
		if r := newBox(0, 0, 300, 200, c.rot).rect(10, 10, 60, 40); r != c.want {
			t.Errorf("turned %d°, the box shows at %+v, want %+v", c.rot, r, c.want)
		}
	}
}
