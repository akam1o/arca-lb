package frr

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// AddStaticRoute adds a static null route for a VIP with a tag
// Supports both IPv4 and IPv6 addresses
func AddStaticRoute(ctx context.Context, vtysh VTYShell, vipAddr string, tag int) error {
	// Parse IP address to determine IP family
	ip := net.ParseIP(vipAddr)
	if ip == nil {
		return fmt.Errorf("invalid VIP address: %s", vipAddr)
	}

	var cidr, routeCmd string
	if ip.To4() == nil {
		// IPv6
		cidr = fmt.Sprintf("%s/128", vipAddr)
		routeCmd = fmt.Sprintf("ipv6 route %s Null0 tag %d", cidr, tag)
	} else {
		// IPv4
		cidr = fmt.Sprintf("%s/32", vipAddr)
		routeCmd = fmt.Sprintf("ip route %s Null0 tag %d", cidr, tag)
	}

	// Use multiple -c flags for better compatibility
	commands := []string{
		"configure terminal",
		routeCmd,
		"end",
	}

	_, err := vtysh.ExecuteCommands(ctx, commands)
	if err != nil {
		return fmt.Errorf("failed to add static route for %s: %w", cidr, err)
	}

	return nil
}

// DeleteStaticRoute removes a static null route for a VIP
// Supports both IPv4 and IPv6 addresses, uses tag to prevent deleting non-arca-lb routes
func DeleteStaticRoute(ctx context.Context, vtysh VTYShell, vipAddr string, tag int) error {
	// Parse IP address to determine IP family
	ip := net.ParseIP(vipAddr)
	if ip == nil {
		return fmt.Errorf("invalid VIP address: %s", vipAddr)
	}

	var cidr, routeCmd string
	if ip.To4() == nil {
		// IPv6
		cidr = fmt.Sprintf("%s/128", vipAddr)
		routeCmd = fmt.Sprintf("no ipv6 route %s Null0 tag %d", cidr, tag)
	} else {
		// IPv4
		cidr = fmt.Sprintf("%s/32", vipAddr)
		routeCmd = fmt.Sprintf("no ip route %s Null0 tag %d", cidr, tag)
	}

	// Use multiple -c flags for better compatibility
	commands := []string{
		"configure terminal",
		routeCmd,
		"end",
	}

	_, err := vtysh.ExecuteCommands(ctx, commands)
	if err != nil {
		return fmt.Errorf("failed to delete static route for %s: %w", cidr, err)
	}

	return nil
}

// ListStaticRoutes lists all static routes with a specific tag
func ListStaticRoutes(ctx context.Context, vtysh VTYShell, tag int) ([]string, error) {
	// Execute 'show ip route static' command
	output, err := vtysh.Execute(ctx, "show ip route static")
	if err != nil {
		return nil, fmt.Errorf("failed to list static routes: %w", err)
	}

	// Parse output to find routes with the specified tag
	routes := parseStaticRoutes(output, tag)
	return routes, nil
}

// parseStaticRoutes parses the output of 'show ip route static' and filters by tag
// Supports both IPv4 and IPv6 routes
func parseStaticRoutes(output string, tag int) []string {
	var routes []string

	// Split output into lines
	lines := strings.Split(output, "\n")

	// Regular expression to match static routes (IPv4 and IPv6)
	// Example IPv4 line: "S>* 10.0.0.100/32 [1/0] is directly connected, Null0, tag 10000, 00:05:23"
	// Example IPv6 line: "S>* 2001:db8::1/128 [1/0] is directly connected, Null0, tag 10000, 00:05:23"
	// Pattern: route prefix (IPv4 or IPv6) followed by tag value
	routePattern := regexp.MustCompile(`^\s*S[>*]*\s+([0-9a-fA-F:\.]+/\d+)\s+.*tag\s+(\d+)`)

	for _, line := range lines {
		matches := routePattern.FindStringSubmatch(line)
		if len(matches) >= 3 {
			routePrefix := matches[1]
			routeTag := matches[2]

			// Check if tag matches
			if routeTag == fmt.Sprintf("%d", tag) {
				routes = append(routes, routePrefix)
			}
		}
	}

	return routes
}

// HasStaticRoute checks if a static route exists for the given VIP
// Supports both IPv4 and IPv6 addresses
func HasStaticRoute(ctx context.Context, vtysh VTYShell, vipAddr string, tag int) (bool, error) {
	routes, err := ListStaticRoutes(ctx, vtysh, tag)
	if err != nil {
		return false, err
	}

	// Parse IP to determine correct CIDR prefix length
	ip := net.ParseIP(vipAddr)
	if ip == nil {
		return false, fmt.Errorf("invalid VIP address: %s", vipAddr)
	}

	var cidr string
	if ip.To4() == nil {
		// IPv6
		cidr = fmt.Sprintf("%s/128", vipAddr)
	} else {
		// IPv4
		cidr = fmt.Sprintf("%s/32", vipAddr)
	}

	// Check if VIP CIDR is in the list
	for _, route := range routes {
		if route == cidr {
			return true, nil
		}
	}

	return false, nil
}
