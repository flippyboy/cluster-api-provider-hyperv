package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
)

func TestHostConfigFromSSH(t *testing.T) {
	spec := testClusterSpec(infrav1.TransportSSH, "")
	secret := &corev1.Secret{Data: map[string][]byte{
		"username":        []byte(" Administrator "),
		"ssh-privatekey":  []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nline\n"),
		"ssh-known-hosts": []byte("hyperv.example ssh-ed25519 AAAA"),
	}}
	cfg, err := HostConfigFrom(spec, secret)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != hyperv.TransportSSH || cfg.Username != "Administrator" || cfg.Port != 0 {
		t.Fatalf("cfg = %#v", cfg)
	}
	if !strings.HasSuffix(string(cfg.PrivateKey), "line\n") {
		t.Fatal("private key was trimmed")
	}
	if cfg.UseHTTPS || cfg.Password != "" {
		t.Fatalf("ssh config picked up winrm fields: %#v", cfg)
	}
}

func TestHostConfigFromSSHRequiresKnownHosts(t *testing.T) {
	spec := testClusterSpec(infrav1.TransportSSH, "")
	secret := &corev1.Secret{Data: map[string][]byte{
		"username":       []byte("Administrator"),
		"ssh-privatekey": []byte("key"),
	}}
	if _, err := HostConfigFrom(spec, secret); err == nil || !strings.Contains(err.Error(), "ssh-known-hosts") {
		t.Fatal(err)
	}
	spec.Host.InsecureSkipHostKeyVerification = true
	cfg, err := HostConfigFrom(spec, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureSkipHostKey || len(cfg.KnownHosts) != 0 {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestHostConfigFromWinRM(t *testing.T) {
	spec := testClusterSpec(infrav1.TransportWinRM, infrav1.WinRMSchemeHTTP)
	spec.Host.Port = 15985
	spec.Host.TLSServerName = "hyperv.example"
	secret := &corev1.Secret{Data: map[string][]byte{
		"username": []byte(` HOST\Administrator `),
		"password": []byte(" s3cret "),
		"ca-cert":  []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"),
	}}
	cfg, err := HostConfigFrom(spec, secret)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != hyperv.TransportWinRM || cfg.UseHTTPS || cfg.Port != 15985 {
		t.Fatalf("cfg = %#v", cfg)
	}
	if cfg.Username != `HOST\Administrator` || cfg.Password != "s3cret" {
		t.Fatalf("auth = %q %q", cfg.Username, cfg.Password)
	}
	if cfg.TLSServerName != "hyperv.example" || len(cfg.CACert) == 0 {
		t.Fatalf("tls = %#v", cfg)
	}
}

func TestHostConfigFromWinRMHTTPSDefaults(t *testing.T) {
	spec := testClusterSpec(infrav1.TransportWinRM, "")
	secret := &corev1.Secret{Data: map[string][]byte{
		"username": []byte("Administrator"),
		"password": []byte("s3cret"),
	}}
	cfg, err := HostConfigFrom(spec, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UseHTTPS || cfg.Port != 0 || cfg.Transport != hyperv.TransportWinRM {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestHostConfigFromWinRMRequiresPassword(t *testing.T) {
	spec := testClusterSpec(infrav1.TransportWinRM, infrav1.WinRMSchemeHTTPS)
	secret := &corev1.Secret{Data: map[string][]byte{"username": []byte("Administrator")}}
	if _, err := HostConfigFrom(spec, secret); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatal(err)
	}
	if _, err := HostConfigFrom(spec, nil); err == nil {
		t.Fatal("expected missing secret error")
	}
}

func testClusterSpec(transport infrav1.Transport, scheme infrav1.WinRMScheme) infrav1.HyperVClusterSpec {
	return infrav1.HyperVClusterSpec{
		Host: infrav1.HostSpec{
			Address:     "hyperv.example",
			Transport:   transport,
			WinRMScheme: scheme,
		},
		CredentialsSecretRef: corev1.LocalObjectReference{Name: "creds"},
		VirtualSwitch:        "External",
		ImagePath:            `C:\images\golden.vhdx`,
	}
}
