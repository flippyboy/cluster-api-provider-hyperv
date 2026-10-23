package controller

import (
	"bytes"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	infrav1 "github.com/flippyboy/cluster-api-provider-hyperv/api/v1alpha1"
	"github.com/flippyboy/cluster-api-provider-hyperv/internal/hyperv"
)

// HostConfigFrom maps a HyperVCluster and its credentials Secret onto a host dial config.
// SSH requires username, ssh-privatekey, and ssh-known-hosts unless host key checks are skipped.
// WinRM requires username and password. NTLM is the only authentication method.
// ca-cert is an optional PEM bundle for WinRM HTTPS.
func HostConfigFrom(spec infrav1.HyperVClusterSpec, secret *corev1.Secret) (hyperv.HostConfig, error) {
	if secret == nil {
		return hyperv.HostConfig{}, fmt.Errorf("credentials secret is missing")
	}
	username := strings.TrimSpace(string(secret.Data["username"]))
	if username == "" {
		return hyperv.HostConfig{}, fmt.Errorf("credentials secret is missing username")
	}
	cfg := hyperv.HostConfig{
		Address:             spec.Host.Address,
		Port:                int(spec.Host.Port),
		Username:            username,
		TLSServerName:       spec.Host.TLSServerName,
		InsecureSkipHostKey: spec.Host.InsecureSkipHostKeyVerification,
		InsecureSkipTLS:     spec.Host.InsecureSkipTLSVerify,
	}
	switch spec.Host.NormalizedTransport() {
	case infrav1.TransportWinRM:
		cfg.Transport = hyperv.TransportWinRM
		cfg.UseHTTPS = spec.Host.NormalizedWinRMScheme() != infrav1.WinRMSchemeHTTP
		password := strings.TrimSpace(string(secret.Data["password"]))
		if password == "" {
			return hyperv.HostConfig{}, fmt.Errorf("credentials secret is missing password")
		}
		cfg.Password = password
		if cert := secret.Data["ca-cert"]; len(bytes.TrimSpace(cert)) > 0 {
			cfg.CACert = cert
		}
	default:
		cfg.Transport = hyperv.TransportSSH
		key := secret.Data["ssh-privatekey"]
		if len(bytes.TrimSpace(key)) == 0 {
			return hyperv.HostConfig{}, fmt.Errorf("credentials secret is missing ssh-privatekey")
		}
		cfg.PrivateKey = key
		if !cfg.InsecureSkipHostKey {
			known := secret.Data["ssh-known-hosts"]
			if len(bytes.TrimSpace(known)) == 0 {
				return hyperv.HostConfig{}, fmt.Errorf("credentials secret is missing ssh-known-hosts")
			}
			cfg.KnownHosts = known
		}
	}
	return cfg, nil
}
