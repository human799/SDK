//go:build !sdkdebug

package sdk

// sdkDebugf discards messages when built without the sdkdebug tag (zero overhead).
func sdkDebugf(format string, args ...any) {}

// SetSDKDebug is a no-op for builds without -tags sdkdebug.
func SetSDKDebug(enabled bool) {}
