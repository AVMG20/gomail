package netguard

import (
	"net/netip"
	"testing"
)

func TestDeprecatedAndTunnelRangesBlocked(t *testing.T) {
	for _, s := range []string{"2002:c0a8:0101::1", "fec0::1", "64:ff9b:1::a00:1", "::10.0.0.1"} {
		if IsPublic(netip.MustParseAddr(s)) {
			t.Errorf("%s treated as public", s)
		}
	}
	if !IsPublic(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Error("public v6 blocked")
	}
}
