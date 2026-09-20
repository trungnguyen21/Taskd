package tools

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// addressPolicy decides which addresses a tool may reach.
//
// Any tool that takes a URL from the model makes the executor an outbound HTTP
// engine, and the model is acting on text written by whoever controls whatever
// it read last. The check belongs on the tool rather than in the prompt.
type addressPolicy struct {
	// allowPrivate opens up private ranges. Off by default; self-hosters who
	// want an agent to reach something on their LAN turn it on deliberately.
	allowPrivate bool
}

func (p *addressPolicy) check(target *url.URL) error {
	switch strings.ToLower(target.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("only http and https urls may be used")
	}

	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("the url has no host")
	}

	addresses, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("could not resolve %s", host)
	}

	// Cloud metadata endpoints are refused even when private addresses are
	// allowed. A self-hoster may want an agent to reach their LAN; nobody
	// legitimately wants one reading instance credentials.
	for _, address := range addresses {
		if isMetadataAddress(address) {
			return fmt.Errorf("refusing to reach %s: it resolves to a cloud metadata endpoint", host)
		}
	}

	if p.allowPrivate {
		return nil
	}

	for _, address := range addresses {
		if !isPublicAddress(address) {
			return fmt.Errorf("refusing to reach %s: it resolves to a private or link-local address", host)
		}
	}
	return nil
}

// isMetadataAddress reports the well-known instance metadata addresses used by
// the major cloud providers.
func isMetadataAddress(address net.IP) bool {
	switch address.String() {
	case "169.254.169.254", "fd00:ec2::254", "169.254.170.2":
		return true
	}
	return false
}

func isPublicAddress(address net.IP) bool {
	return !(address.IsLoopback() || address.IsPrivate() || address.IsUnspecified() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast())
}
