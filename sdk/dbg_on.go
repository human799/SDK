//go:build sdkdebug

package sdk

import (
	"log"
	"sync/atomic"
)

var sdkDebugOn atomic.Bool

func init() {
	sdkDebugOn.Store(true)
}

// SetSDKDebug toggles runtime debug logs for builds produced with -tags sdkdebug.
// Use this to silence verbose logs in a debug-capable AAR without rebuilding.
func SetSDKDebug(enabled bool) {
	sdkDebugOn.Store(enabled)
}

func sdkDebugf(format string, args ...any) {
	if !sdkDebugOn.Load() {
		return
	}
	if len(args) == 0 {
		log.Printf("[proxysystem-sdk] %s", format)
		return
	}
	log.Printf("[proxysystem-sdk] "+format, args...)
}
