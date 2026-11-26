package cloudinit

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStaticNetworkConfig(t *testing.T) {
	got, err := StaticNetworkConfig(StaticNetwork{
		MAC:       "0e:f5:62:0c:2d:f9",
		Interface: "eth0",
		Addresses: []StaticAddress{
			{Address: "10.16.60.140", Prefix: 24, Gateway: "10.16.60.1"},
			{Address: "10.16.61.140", Prefix: 24, Gateway: "10.16.60.1"},
			{Address: "fd00::140", Prefix: 64, Gateway: "fd00::1"},
		},
		Nameservers:   []string{"10.16.60.1", "1.1.1.1"},
		SearchDomains: []string{"lab.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version   int `yaml:"version"`
		Ethernets map[string]struct {
			Match struct {
				MACAddress string `yaml:"macaddress"`
			} `yaml:"match"`
			SetName   string   `yaml:"set-name"`
			DHCP4     bool     `yaml:"dhcp4"`
			DHCP6     bool     `yaml:"dhcp6"`
			Addresses []string `yaml:"addresses"`
			Routes    []struct {
				To  string `yaml:"to"`
				Via string `yaml:"via"`
			} `yaml:"routes"`
			Nameservers struct {
				Addresses []string `yaml:"addresses"`
				Search    []string `yaml:"search"`
			} `yaml:"nameservers"`
		} `yaml:"ethernets"`
	}
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	eth, ok := doc.Ethernets["id0"]
	if !ok || doc.Version != 2 {
		t.Fatalf("unexpected doc:\n%s", got)
	}
	if eth.Match.MACAddress != "0e:f5:62:0c:2d:f9" || eth.SetName != "eth0" || eth.DHCP4 || eth.DHCP6 {
		t.Fatalf("unexpected nic:\n%s", got)
	}
	if strings.Join(eth.Addresses, ",") != "10.16.60.140/24,10.16.61.140/24,fd00::140/64" {
		t.Fatalf("addresses = %v", eth.Addresses)
	}
	if len(eth.Routes) != 2 || eth.Routes[0].Via != "10.16.60.1" || eth.Routes[1].Via != "fd00::1" {
		t.Fatalf("routes = %+v", eth.Routes)
	}
	if strings.Join(eth.Nameservers.Addresses, ",") != "10.16.60.1,1.1.1.1" || eth.Nameservers.Search[0] != "lab.example" {
		t.Fatalf("nameservers = %+v", eth.Nameservers)
	}
}

func TestStaticNetworkConfigRejects(t *testing.T) {
	ok := StaticAddress{Address: "10.0.0.5", Prefix: 24, Gateway: "10.0.0.1"}
	cases := map[string]StaticNetwork{
		"no mac":           {Addresses: []StaticAddress{ok}},
		"no addresses":     {MAC: "00:15:5d:aa:bb:cc"},
		"bad interface":    {MAC: "00:15:5d:aa:bb:cc", Interface: "eth 0", Addresses: []StaticAddress{ok}},
		"bad address":      {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{{Address: "10.0.0", Prefix: 24}}},
		"prefix too big":   {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{{Address: "10.0.0.5", Prefix: 33}}},
		"prefix zero":      {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{{Address: "10.0.0.5", Prefix: 0}}},
		"gateway family":   {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{{Address: "10.0.0.5", Prefix: 24, Gateway: "fd00::1"}}},
		"two gateways":     {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{ok, {Address: "10.0.1.5", Prefix: 24, Gateway: "10.0.1.1"}}},
		"bad nameserver":   {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{ok}, Nameservers: []string{"dns.example"}},
		"bad gateway text": {MAC: "00:15:5d:aa:bb:cc", Addresses: []StaticAddress{{Address: "10.0.0.5", Prefix: 24, Gateway: "router"}}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := StaticNetworkConfig(in); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
