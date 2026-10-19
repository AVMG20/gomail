//go:build !windows

package secrets

// protectKey is a no-op outside Windows: the key file is mode 0600 inside a
// 0700 directory.
func protectKey(key []byte) ([]byte, error) { return key, nil }

func unprotectKey(data []byte) ([]byte, error) { return data, nil }
