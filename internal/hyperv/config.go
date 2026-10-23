package hyperv

// Transport is how the controller reaches the host.
type Transport string

const (
	// TransportSSH runs PowerShell over OpenSSH.
	TransportSSH Transport = "SSH"
	// TransportWinRM runs PowerShell over WinRM with NTLM.
	TransportWinRM Transport = "WinRM"
)

// HostConfig is the dial input. It is independent of the Kubernetes API.
type HostConfig struct {
	Address             string
	Port                int
	Transport           Transport
	UseHTTPS            bool
	InsecureSkipHostKey bool
	InsecureSkipTLS     bool
	TLSServerName       string
	Username            string
	PrivateKey          []byte
	KnownHosts          []byte
	Password            string
	CACert              []byte
}

// NormalizedTransport returns SSH when transport is empty.
func (c HostConfig) NormalizedTransport() Transport {
	if c.Transport == TransportWinRM {
		return TransportWinRM
	}
	return TransportSSH
}

// DialPort returns the TCP port, applying the transport default when Port is 0.
func (c HostConfig) DialPort() int {
	if c.Port != 0 {
		return c.Port
	}
	if c.NormalizedTransport() == TransportWinRM {
		if c.UseHTTPS {
			return 5986
		}
		return 5985
	}
	return 22
}
