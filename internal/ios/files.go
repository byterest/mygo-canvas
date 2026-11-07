//go:build ios

package ios

import (
	"os"

	"github.com/egoist/mygo/internal/platform"
)

// NewFileWatch watches nothing: an app of iOS is the only one writing its
// files, but for the Files app's, which come later.
func (b *Backend) NewFileWatch(notify func(platform.FileWatchNotice)) (platform.FileWatch, error) {
	return noWatch{}, nil
}

// ReplaceFile replaces target with replacement by renaming it over it, in
// the app's own container.
func (b *Backend) ReplaceFile(target, replacement string) error {
	return os.Rename(replacement, target)
}

type noWatch struct{}

func (noWatch) Add(platform.FileWatchTarget) error { return nil }
func (noWatch) Remove(uint64)                      {}
func (noWatch) Close()                             {}
