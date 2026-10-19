package secrets

import "runtime"

func isWindows() bool { return runtime.GOOS == "windows" }
