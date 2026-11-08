package pdf

import "syscall"

const pdfiumLibraryName = "pdfium.dll"

// dlopen loads a library by its full path, so that no other directory is
// searched for it.
func dlopen(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	return uintptr(h), err
}

func dlsym(lib uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(lib), name)
}

// ccall calls a function of the library. PDFium's take integers and
// pointers only, which syscall passes on every Windows, stdcall on x86.
func ccall(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}
