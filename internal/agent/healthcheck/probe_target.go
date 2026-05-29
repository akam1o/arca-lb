package healthcheck

import (
	"fmt"
	"net"
)

func validateProbeTarget(target string) error {
	if ip := net.ParseIP(target); ip == nil {
		return fmt.Errorf("invalid health check target address: %q", target)
	}
	return nil
}
