//go:build ios

package ios

import "github.com/egoist/mygo/internal/platform"

// noPage is the web page a window has none of: windows of iOS show native
// UI only.
type noPage struct{}

func (noPage) WebViewHandle() uintptr               { return 0 }
func (noPage) LoadURL(string)                       {}
func (noPage) LoadHTML(string, string)              {}
func (noPage) LoadFile(string, string)              {}
func (noPage) Reload(bool)                          {}
func (noPage) StopLoading()                         {}
func (noPage) GoBack()                              {}
func (noPage) GoForward()                           {}
func (noPage) CanGoBack() bool                      { return false }
func (noPage) CanGoForward() bool                   { return false }
func (noPage) URL() string                          { return "" }
func (noPage) IsLoading() bool                      { return false }
func (noPage) Eval(string)                          {}
func (noPage) SetUserScripts([]platform.UserScript) {}
func (noPage) SetContentRules([]any)                {}
func (noPage) SetZoom(float64)                      {}
func (noPage) Zoom() float64                        { return 1 }
func (noPage) SetUserAgent(string)                  {}
func (noPage) UserAgent() string                    { return "" }
func (noPage) ProcessID() int                       { return 0 }
func (noPage) IsAudible() bool                      { return false }
func (noPage) SetAudioMuted(bool)                   {}
func (noPage) IsAudioMuted() bool                   { return false }
func (noPage) TogglePictureInPicture()              {}
func (noPage) SessionState() []byte                 { return nil }
func (noPage) RestoreSessionState([]byte) bool      { return false }
func (noPage) OpenDevTools()                        {}
func (noPage) CloseDevTools()                       {}
func (noPage) IsDevToolsOpened() bool               { return false }
func (noPage) DroppedFiles() []string               { return nil }
func (noPage) Print()                               {}
func (noPage) CaptureState(platform.CaptureKind) platform.CaptureState {
	return platform.CaptureState(0)
}
func (noPage) SetCaptureState(platform.CaptureKind, platform.CaptureState) {}
func (noPage) PictureInPictureActive(cb func(bool))                        { cb(false) }
func (noPage) CallAsyncFunction(_ string, cb func(string, error)) {
	cb("", platform.ErrUnsupported)
}
func (noPage) CapturePage(cb func([]byte, error)) { cb(nil, platform.ErrUnsupported) }
func (noPage) PrintToPDF(_ platform.PDFOptions, cb func([]byte, error)) {
	cb(nil, platform.ErrUnsupported)
}
