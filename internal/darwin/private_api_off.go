//go:build darwin && !ios && mygo_publicapi

package darwin

const allowPrivateAPI = false

const (
	webkitDisplayCaptureKey    = ""
	webkitDisplayCaptureSetter = ""
)
