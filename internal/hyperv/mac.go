package hyperv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// RandomMAC returns a locally administered unicast MAC, lowercase with colons.
func RandomMAC() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate mac: %w", err)
	}
	b[0] = (b[0] | 0x02) & 0xfe
	return ColonMAC(hex.EncodeToString(b[:]))
}

// ColonMAC returns the lowercase colon form of a MAC address.
func ColonMAC(mac string) (string, error) {
	raw, err := rawMAC(mac)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i := 0; i < len(raw); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(raw[i : i+2])
	}
	return b.String(), nil
}

// HyperVMAC returns the 12 uppercase hex digits Set-VMNetworkAdapter accepts.
func HyperVMAC(mac string) (string, error) {
	raw, err := rawMAC(mac)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(raw), nil
}

func rawMAC(mac string) (string, error) {
	var b strings.Builder
	for _, r := range mac {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r >= 'A' && r <= 'F':
			b.WriteRune(r + ('a' - 'A'))
		case r == ':' || r == '-' || r == '.' || r == ' ':
			continue
		default:
			return "", fmt.Errorf("invalid mac address %q", mac)
		}
	}
	raw := b.String()
	if len(raw) != 12 {
		return "", fmt.Errorf("invalid mac address %q", mac)
	}
	return raw, nil
}

// ProviderID formats a Hyper-V VM GUID as hyperv://<guid>.
func ProviderID(vmID string) (string, error) {
	id := strings.ToLower(strings.Trim(strings.TrimSpace(vmID), "{}"))
	if id == "" {
		return "", fmt.Errorf("vm id is empty")
	}
	return "hyperv://" + id, nil
}

// IPv4Addresses keeps globally unicast IPv4 addresses.
// Link-local 169.254.0.0/16, loopback, and IPv6 addresses are dropped.
func IPv4Addresses(addrs []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range addrs {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil || !ip4.IsGlobalUnicast() {
			continue
		}
		s := ip4.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
