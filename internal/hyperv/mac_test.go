package hyperv

import (
	"net"
	"strings"
	"testing"
)

func TestRandomMACIsLocalUnicast(t *testing.T) {
	mac, err := RandomMAC()
	if err != nil {
		t.Fatal(err)
	}
	hw, err := net.ParseMAC(mac)
	if err != nil {
		t.Fatal(err)
	}
	if hw[0]&0x02 == 0 || hw[0]&0x01 != 0 {
		t.Fatalf("not locally administered unicast: %s", mac)
	}
	if mac != strings.ToLower(mac) || strings.Count(mac, ":") != 5 {
		t.Fatal(mac)
	}
	hyperv, err := HyperVMAC(mac)
	if err != nil {
		t.Fatal(err)
	}
	if hyperv != strings.ToUpper(strings.ReplaceAll(mac, ":", "")) {
		t.Fatal(hyperv)
	}
}

func TestColonMAC(t *testing.T) {
	got, err := ColonMAC("00-15-5D-AA-BB-CC")
	if err != nil {
		t.Fatal(err)
	}
	if got != "00:15:5d:aa:bb:cc" {
		t.Fatal(got)
	}
	if _, err := ColonMAC("00:11"); err == nil {
		t.Fatal("expected error")
	}
}

func TestProviderID(t *testing.T) {
	got, err := ProviderID("{AABBCCDD-EEFF-0011-2233-445566778899}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hyperv://aabbccdd-eeff-0011-2233-445566778899" {
		t.Fatal(got)
	}
	if _, err := ProviderID("  "); err == nil {
		t.Fatal("expected error")
	}
}

func TestIPv4AddressesFilters(t *testing.T) {
	got := IPv4Addresses([]string{
		"10.1.2.3",
		"169.254.4.5",
		"127.0.0.1",
		"fe80::1",
		"::1",
		"8.8.8.8",
		"10.1.2.3",
		"not-an-ip",
		"0.0.0.0",
	})
	want := []string{"10.1.2.3", "8.8.8.8"}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
}

func TestVMName(t *testing.T) {
	if got := VMName("demo", "cp-0"); got != "capi-demo-cp-0" {
		t.Fatal(got)
	}
	if got := VMName("My Cluster", "node_1"); got != "capi-My-Cluster-node-1" {
		t.Fatal(got)
	}
	long := strings.Repeat("a", 200)
	got := VMName(long, long)
	if len(got) > 100 || strings.HasSuffix(got, "-") || !strings.HasPrefix(got, "capi-") {
		t.Fatalf("%d %s", len(got), got)
	}
}

func TestJoinWin(t *testing.T) {
	if got := JoinWin(`C:\VMs\vm\`, "cidata.iso"); got != `C:\VMs\vm\cidata.iso` {
		t.Fatal(got)
	}
	if got := JoinWin(`C:/VMs/vm`, "cidata.iso"); got != `C:/VMs/vm\cidata.iso` {
		t.Fatal(got)
	}
}
