//go:build pdfium && !windows

package pdf

import (
	"os"

	"github.com/ebitengine/purego"
)

const pdfiumLibraryName = "libpdfium.dylib"

func dlopen(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func dlsym(lib uintptr, name string) (uintptr, error) { return purego.Dlsym(lib, name) }

func ccall(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := purego.SyscallN(fn, args...)
	return r
}

// Built with the pdfium tag, a program opens its PDFs with PDFium when
// MYGO_PDFIUM names the library, to see it as on Windows.
func init() {
	if os.Getenv(pdfiumLibraryEnvar) != "" {
		openEngine = openPDFium
	}
}
