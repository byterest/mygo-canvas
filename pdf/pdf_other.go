//go:build !darwin && !linux && !windows

package pdf

// Elsewhere, as in a browser, there is no PDF engine of the system's.
func open([]byte) (engine, error) { return nil, ErrUnsupported }
