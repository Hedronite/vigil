package server

import (
	"fmt"
	"net/netip"
)

// CheckBind reports whether listen may be used with allowNonLoopback.
func CheckBind(listen string, allowNonLoopback bool) error {
	ap, err := netip.ParseAddrPort(listen)
	if err != nil {
		return fmt.Errorf("listen address is not valid")
	}
	if ap.Addr().IsLoopback() {
		return nil
	}
	if !allowNonLoopback {
		return fmt.Errorf("a non-loopback listen needs --allow-nonloopback")
	}
	return nil
}
