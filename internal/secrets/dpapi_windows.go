package secrets

import (
	"bytes"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// protectKey encrypts the fallback store's key with DPAPI (bound to the
// Windows user account), because file permissions don't protect it there.
func protectKey(key []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(key)), Data: &key[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(dpapiPrefix), unsafe.Slice(out.Data, out.Size)...), nil
}

func unprotectKey(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(dpapiPrefix)) {
		return data, nil // legacy plain key
	}
	enc := data[len(dpapiPrefix):]
	if len(enc) == 0 {
		return nil, errors.New("empty protected key")
	}
	in := windows.DataBlob{Size: uint32(len(enc)), Data: &enc[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
