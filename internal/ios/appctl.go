//go:build ios

package ios

import "github.com/egoist/mygo/internal/platform"

// appController is the app's: an app of iOS has no Dock, menus or recent
// documents of the system, which do nothing, as the unsupported backend's.
type appController struct{ platform.AppController }

func (b *Backend) App() platform.AppController { return appController{b.Backend.App()} }

func (appController) AddRecentDocument(string) error { return nil }
func (appController) ClearRecentDocuments() error    { return nil }
func (appController) IsActive() bool {
	return sendInt(send(class("UIApplication"), "sharedApplication"), "applicationState") == 0
}

// PreferredLanguages returns the languages the user prefers, as BCP 47
// tags.
func (appController) PreferredLanguages() []string {
	var langs []string
	withPool(func() {
		arr := send(class("NSLocale"), "preferredLanguages")
		for i := range sendInt(arr, "count") {
			langs = append(langs, goString(send(arr, "objectAtIndex:", uintptr(i))))
		}
	})
	if len(langs) == 0 {
		return []string{"en-US"}
	}
	return langs
}
