package hyperv

import (
	"reflect"
	"strings"
	"testing"

	"github.com/masterzen/winrm"
)

func TestBuildWinRMEndpoint(t *testing.T) {
	ca := []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
	cases := []struct {
		name   string
		cfg    HostConfig
		port   int
		https  bool
		skip   bool
		server string
	}{
		{
			name:  "http default",
			cfg:   HostConfig{Address: "hv.example"},
			port:  5985,
			https: false,
		},
		{
			name:  "https default",
			cfg:   HostConfig{Address: "hv.example", UseHTTPS: true, CACert: ca, TLSServerName: "hv.example"},
			port:  5986,
			https: true,
		},
		{
			name:  "custom http port",
			cfg:   HostConfig{Address: "10.0.0.5", Port: 15985, InsecureSkipTLS: true},
			port:  15985,
			https: false,
			skip:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep := buildWinRMEndpoint(tc.cfg)
			if ep.Host != tc.cfg.Address || ep.Port != tc.port || ep.HTTPS != tc.https || ep.Insecure != tc.skip {
				t.Fatalf("%+v", ep)
			}
			if ep.TLSServerName != tc.cfg.TLSServerName {
				t.Fatal(ep.TLSServerName)
			}
			if string(ep.CACert) != string(tc.cfg.CACert) {
				t.Fatal("ca cert")
			}
			scheme := "http"
			if tc.https {
				scheme = "https"
			}
			wantPrefix := scheme + "://"
			if ep.Host == "" || !hasURLScheme(ep, wantPrefix) {
				t.Fatalf("endpoint %+v", ep)
			}
		})
	}
}

func hasURLScheme(ep *winrm.Endpoint, scheme string) bool {
	if scheme == "https://" && !ep.HTTPS {
		return false
	}
	if scheme == "http://" && ep.HTTPS {
		return false
	}
	return true
}

func TestWinRMParamsSelectNTLMWithoutMutatingDefaults(t *testing.T) {
	if winrm.DefaultParameters.TransportDecorator != nil {
		t.Fatal("DefaultParameters.TransportDecorator is already set")
	}
	timeout := winrm.DefaultParameters.Timeout
	params, err := winRMClientParams()
	if err != nil {
		t.Fatal(err)
	}
	if winrm.DefaultParameters.TransportDecorator != nil {
		t.Fatal("mutated DefaultParameters.TransportDecorator")
	}
	if winrm.DefaultParameters.Timeout != timeout {
		t.Fatal("mutated DefaultParameters.Timeout")
	}
	if params.TransportDecorator == nil {
		t.Fatal("missing decorator")
	}
	tr := params.TransportDecorator()
	enc, ok := tr.(*winrm.Encryption)
	if !ok {
		t.Fatalf("transport %T", tr)
	}
	protocol := reflect.ValueOf(enc).Elem().FieldByName("protocol")
	if protocol.String() != "ntlm" {
		t.Fatalf("protocol %s", protocol.String())
	}
	client, err := newWinRMClient(HostConfig{
		Address:  "hv.example",
		UseHTTPS: true,
		Username: `HV\Administrator`,
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
	if winrm.DefaultParameters.TransportDecorator != nil {
		t.Fatal("newWinRMClient mutated DefaultParameters")
	}
}

func TestNewWinRMClientRequiresPassword(t *testing.T) {
	_, err := newWinRMClient(HostConfig{Address: "hv", Username: "Administrator"})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatal(err)
	}
}
