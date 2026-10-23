package v1alpha1

import "testing"

func TestEffectivePort(t *testing.T) {
	tests := []struct {
		name string
		host HostSpec
		want int32
	}{
		{name: "ssh default", host: HostSpec{}, want: 22},
		{name: "ssh custom", host: HostSpec{Port: 2222}, want: 2222},
		{name: "winrm https default", host: HostSpec{Transport: TransportWinRM}, want: 5986},
		{name: "winrm http default", host: HostSpec{Transport: TransportWinRM, WinRMScheme: WinRMSchemeHTTP}, want: 5985},
		{name: "winrm custom", host: HostSpec{Transport: TransportWinRM, WinRMScheme: WinRMSchemeHTTP, Port: 15985}, want: 15985},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.host.EffectivePort(); got != tt.want {
				t.Fatalf("EffectivePort() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNormalizedTransportAndScheme(t *testing.T) {
	var host HostSpec
	if host.NormalizedTransport() != TransportSSH {
		t.Fatalf("transport = %s", host.NormalizedTransport())
	}
	if host.NormalizedWinRMScheme() != WinRMSchemeHTTPS {
		t.Fatalf("scheme = %s", host.NormalizedWinRMScheme())
	}
	host.Transport = TransportWinRM
	host.WinRMScheme = WinRMSchemeHTTP
	if host.NormalizedTransport() != TransportWinRM || host.NormalizedWinRMScheme() != WinRMSchemeHTTP {
		t.Fatalf("host = %#v", host)
	}
}
