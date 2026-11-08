//go:build pdfium && !windows

package pdf

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// With the pdfium tag and MYGO_PDFIUM naming the library, every test of
// the package runs on PDFium, as on Windows (pdfium_dl_unix.go), and these
// compare it with the system's engine:
//
//	MYGO_PDFIUM=…/libpdfium.dylib go test -tags pdfium ./pdf

// both opens a PDF with the system's engine and with PDFium.
func both(t *testing.T, data []byte) (sys, fz engine) {
	t.Helper()
	if !loadPDFium() {
		t.Skip("no PDFium: set " + pdfiumLibraryEnvar)
	}
	sys, err := open(data)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(sys.close)
	fz, err = openPDFium(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fz.close)
	return sys, fz
}

func nearR(a, b Rect, tol float64) bool {
	return near(a.X, b.X, tol) && near(a.Y, b.Y, tol) && near(a.W, b.W, tol) && near(a.H, b.H, tol)
}

// PDFium gives what PDFKit gives of the test PDF: its sizes, its text and
// where each character is, and its links.
func TestPDFiumAsTheSystem(t *testing.T) {
	sys, fz := both(t, testPDF())
	if sys.pages() != fz.pages() {
		t.Fatalf("%d pages, PDFium %d", sys.pages(), fz.pages())
	}
	for i := range sys.pages() {
		if s, f := sys.size(i), fz.size(i); !near(s.W, f.W, 0.01) || !near(s.H, f.H, 0.01) {
			t.Errorf("page %d is %v, PDFium %v", i, s, f)
		}
		st, _ := sys.text(i)
		ft, _ := fz.text(i)
		// The words, whatever space and lines the engines put between
		// them, and the box of each of their characters.
		words := func(t Text) (w []string, boxes []Rect) {
			var cur []rune
			for j, r := range t.Runes {
				if r == ' ' || r == '\n' {
					if len(cur) > 0 {
						w = append(w, string(cur))
					}
					cur = nil
					continue
				}
				cur = append(cur, r)
				boxes = append(boxes, t.Boxes[j])
			}
			if len(cur) > 0 {
				w = append(w, string(cur))
			}
			return w, boxes
		}
		sw, sb := words(st)
		fw, fb := words(ft)
		if strings.Join(sw, " ") != strings.Join(fw, " ") {
			t.Errorf("page %d says %q, PDFium %q", i, sw, fw)
			continue
		}
		for j := range sb {
			// The tops and bottoms of the fonts' boxes differ a little.
			if !near(sb[j].X, fb[j].X, 1) || !near(sb[j].X+sb[j].W, fb[j].X+fb[j].W, 1.5) || !near(sb[j].Y, fb[j].Y, 4) || !near(sb[j].H, fb[j].H, 6) {
				t.Errorf("page %d character %d is at %+v, PDFium %+v", i, j, sb[j], fb[j])
			}
		}
		sl, _ := sys.links(i)
		fl, _ := fz.links(i)
		if len(sl) != len(fl) {
			t.Errorf("page %d links %+v, PDFium %+v", i, sl, fl)
			continue
		}
		for j := range sl {
			s, f := sl[j], fl[j]
			if s.URL != f.URL || (s.URL == "" && s.Page != f.Page) || !nearR(s.Rect, f.Rect, 0.5) {
				t.Errorf("page %d link %+v, PDFium %+v", i, s, f)
			}
		}
	}
}

// colorPDF is a page of 200 by 100 with a red, a green and a blue square
// along it.
func colorPDF() []byte {
	content := "1 0 0 rg 10 10 50 80 re f\n0 1 0 rg 75 10 50 80 re f\n0 0 1 rg 140 10 50 80 re f\n"
	objs := []string{
		1: "<< /Type /Catalog /Pages 2 0 R >>",
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents 4 0 R >>",
		4: stream(content),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
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

// PDFium's pixels are RGBA, opaque, and those PDFKit renders.
func TestPDFiumRendersAsTheSystem(t *testing.T) {
	for _, c := range []struct {
		data  []byte
		page  int
		scale float64
		clip  image.Rectangle
	}{
		{colorPDF(), 0, 2, image.Rectangle{}},
		{colorPDF(), 0, 3, image.Rect(100, 50, 400, 200)},
		{testPDF(), 0, 1, image.Rectangle{}},
		{testPDF(), 1, 2, image.Rectangle{}},
	} {
		sys, fz := both(t, c.data)
		s := sys.size(c.page)
		clip := c.clip
		if clip.Empty() {
			clip = image.Rect(0, 0, int(s.W*c.scale+0.5), int(s.H*c.scale+0.5))
		}
		a, err := sys.render(c.page, c.scale, clip)
		if err != nil {
			t.Fatal(err)
		}
		b, err := fz.render(c.page, c.scale, clip)
		if err != nil {
			t.Fatal(err)
		}
		if a.Bounds() != b.Bounds() {
			t.Fatalf("rendered %v, PDFium %v", a.Bounds(), b.Bounds())
		}
		off := 0
		for i := 0; i < len(a.Pix); i += 4 {
			d := 0
			for k := range 4 {
				d = max(d, absInt(int(a.Pix[i+k])-int(b.Pix[i+k])))
			}
			if d > 64 { // the edges of glyphs and shapes differ a little
				off++
			}
		}
		if n := len(a.Pix) / 4; off > n/100 {
			t.Errorf("page %d at %v, %v: %d of %d pixels differ", c.page, c.scale, c.clip, off, n)
		}
	}
	// The channels are in the order of image.RGBA.
	_, fz := both(t, colorPDF())
	img, _ := fz.render(0, 1, image.Rect(0, 0, 200, 100))
	for _, p := range []struct {
		x, y int
		want [4]uint8
	}{{30, 50, [4]uint8{255, 0, 0, 255}}, {100, 50, [4]uint8{0, 255, 0, 255}}, {165, 50, [4]uint8{0, 0, 255, 255}}, {5, 5, [4]uint8{255, 255, 255, 255}}} {
		o := img.PixOffset(p.x, p.y)
		if got := [4]uint8(img.Pix[o : o+4]); got != p.want {
			t.Errorf("at %d, %d PDFium gives %v, want %v", p.x, p.y, got, p.want)
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// PDF_MEMORY=file.pdf tells how much each engine takes to open a PDF and
// read all of its pages: rendering each, and its text and links.
func TestPDFMemory(t *testing.T) {
	path := os.Getenv("PDF_MEMORY")
	if path == "" {
		t.Skip("PDF_MEMORY not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loadPDFium() {
		t.Skip("no PDFium")
	}
	for _, e := range []struct {
		name string
		open func([]byte) (engine, error)
	}{{"system", open}, {"PDFium", openPDFium}} {
		if only := os.Getenv("PDF_MEMORY_ENGINE"); only != "" && only != e.name {
			continue
		}
		before := rss()
		d, err := e.open(data)
		if err != nil {
			t.Fatal(err)
		}
		opened := rss()
		peak := opened
		for i := range d.pages() {
			if _, err := d.render(i, 1, image.Rect(0, 0, int(d.size(i).W), int(d.size(i).H))); err != nil {
				t.Fatal(err)
			}
			d.text(i)
			d.links(i)
			if i%10 == 0 {
				runtime.GC()
				peak = max(peak, rss())
			}
		}
		runtime.GC()
		read := rss()
		d.close()
		runtime.GC()
		t.Logf("%-6s %d pages: open +%.1f MB, after reading all +%.1f MB (peak +%.1f MB), closed +%.1f MB",
			e.name, d.pages(), mb(opened-before), mb(read-before), mb(peak-before), mb(rss()-before))
	}
}

func mb(b int64) float64 { return float64(b) / (1 << 20) }

// rss returns how much memory the process takes now, in bytes.
func rss() int64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	kb, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return kb << 10
}
