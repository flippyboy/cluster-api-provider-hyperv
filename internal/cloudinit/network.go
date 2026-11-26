package cloudinit

import (
	"fmt"
	"net/netip"
	"strings"

	"gopkg.in/yaml.v3"
)

// StaticAddress is one address allocated to the NIC, usually from an
// IPAM IPAddress.
type StaticAddress struct {
	Address string
	Prefix  int
	Gateway string
}

// StaticNetwork is a NIC configured without DHCP.
type StaticNetwork struct {
	// MAC is the lowercase colon form stored on the HyperVMachine.
	MAC string
	// Interface renames the NIC when it is not empty. kube-vip binds to it.
	Interface     string
	Addresses     []StaticAddress
	Nameservers   []string
	SearchDomains []string
}

type netplanFile struct {
	Version   int                        `yaml:"version"`
	Ethernets map[string]netplanEthernet `yaml:"ethernets"`
}

type netplanEthernet struct {
	Match       netplanMatch        `yaml:"match"`
	SetName     string              `yaml:"set-name,omitempty"`
	DHCP4       bool                `yaml:"dhcp4"`
	DHCP6       bool                `yaml:"dhcp6"`
	Addresses   []string            `yaml:"addresses"`
	Routes      []netplanRoute      `yaml:"routes,omitempty"`
	Nameservers *netplanNameservers `yaml:"nameservers,omitempty"`
}

type netplanMatch struct {
	MACAddress string `yaml:"macaddress"`
}

type netplanRoute struct {
	To  string `yaml:"to"`
	Via string `yaml:"via"`
}

type netplanNameservers struct {
	Addresses []string `yaml:"addresses,omitempty"`
	Search    []string `yaml:"search,omitempty"`
}

// StaticNetworkConfig is a netplan v2 file that sets the NIC with this MAC
// to fixed addresses. Each address family gets one default route, from the
// first address in that family that has a gateway.
func StaticNetworkConfig(n StaticNetwork) ([]byte, error) {
	if strings.TrimSpace(n.MAC) == "" {
		return nil, fmt.Errorf("mac address is empty")
	}
	if n.Interface != "" && !ifaceName.MatchString(n.Interface) {
		return nil, fmt.Errorf("interface %q is invalid", n.Interface)
	}
	if len(n.Addresses) == 0 {
		return nil, fmt.Errorf("static network config has no addresses")
	}
	eth := netplanEthernet{
		Match:   netplanMatch{MACAddress: n.MAC},
		SetName: n.Interface,
	}
	gateways := map[bool]netip.Addr{}
	for _, a := range n.Addresses {
		addr, err := netip.ParseAddr(a.Address)
		if err != nil {
			return nil, fmt.Errorf("address %q: %w", a.Address, err)
		}
		if a.Prefix < 1 || a.Prefix > addr.BitLen() {
			return nil, fmt.Errorf("address %s: prefix %d is out of range", addr, a.Prefix)
		}
		eth.Addresses = append(eth.Addresses, netip.PrefixFrom(addr, a.Prefix).String())
		if a.Gateway == "" {
			continue
		}
		gw, err := netip.ParseAddr(a.Gateway)
		if err != nil {
			return nil, fmt.Errorf("gateway %q: %w", a.Gateway, err)
		}
		if gw.Is4() != addr.Is4() {
			return nil, fmt.Errorf("gateway %s is not in the same family as %s", gw, addr)
		}
		v4 := addr.Is4()
		if prev, ok := gateways[v4]; ok {
			if prev != gw {
				return nil, fmt.Errorf("addresses have different default gateways %s and %s", prev, gw)
			}
			continue
		}
		gateways[v4] = gw
		eth.Routes = append(eth.Routes, netplanRoute{To: "default", Via: gw.String()})
	}
	for _, ns := range n.Nameservers {
		if _, err := netip.ParseAddr(ns); err != nil {
			return nil, fmt.Errorf("nameserver %q: %w", ns, err)
		}
	}
	if len(n.Nameservers) > 0 || len(n.SearchDomains) > 0 {
		eth.Nameservers = &netplanNameservers{Addresses: n.Nameservers, Search: n.SearchDomains}
	}
	out, err := yaml.Marshal(netplanFile{Version: 2, Ethernets: map[string]netplanEthernet{"id0": eth}})
	if err != nil {
		return nil, fmt.Errorf("encode network config: %w", err)
	}
	return out, nil
}
