package healthcheck

import (
	"fmt"
	"net"
)

func validatePingTarget(target string) error {
	if ip := net.ParseIP(target); ip == nil {
		return fmt.Errorf("invalid ping target address: %q", target)
	}
	return nil
}
