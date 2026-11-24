//go:build darwin && !ios && !mygo_publicapi

package darwin

const allowPrivateAPI = true

const (
	webkitDisplayCaptureKey    = "_displayCaptureState"
	webkitDisplayCaptureSetter = "_setDisplayCaptureState:completionHandler:"
)
