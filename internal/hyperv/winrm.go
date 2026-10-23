package hyperv

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/masterzen/winrm"
)

const winRMTimeout = 2 * time.Minute

// winrmSession runs commands with NTLM message encryption.
// Upload is rejected. Client.Upload sends write-chunk instead, because
// calling execScript from Upload would recurse through the session.
type winrmSession struct {
	client *winrm.Client
}

func dialWinRM(cfg HostConfig) (*winrmSession, error) {
	client, err := newWinRMClient(cfg)
	if err != nil {
		return nil, err
	}
	return &winrmSession{client: client}, nil
}

// buildWinRMEndpoint builds the endpoint without dialing.
// Port 0 selects 5985 for HTTP and 5986 for HTTPS.
func buildWinRMEndpoint(cfg HostConfig) *winrm.Endpoint {
	port := cfg.Port
	if port == 0 {
		port = 5985
		if cfg.UseHTTPS {
			port = 5986
		}
	}
	endpoint := winrm.NewEndpoint(
		cfg.Address,
		port,
		cfg.UseHTTPS,
		cfg.InsecureSkipTLS,
		cfg.CACert,
		nil,
		nil,
		winRMTimeout,
	)
	endpoint.TLSServerName = cfg.TLSServerName
	return endpoint
}

// winRMClientParams copies the library defaults and selects NTLM encryption.
// DefaultParameters is a package-level pointer and must not be mutated.
func winRMClientParams() (*winrm.Parameters, error) {
	params := *winrm.DefaultParameters
	params.Timeout = "PT120S"
	encryption, err := winrm.NewEncryption("ntlm")
	if err != nil {
		return nil, fmt.Errorf("winrm ntlm: %w", err)
	}
	params.TransportDecorator = func() winrm.Transporter {
		return encryption
	}
	return &params, nil
}

func newWinRMClient(cfg HostConfig) (*winrm.Client, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("winrm host address is empty")
	}
	if cfg.Username == "" {
		return nil, fmt.Errorf("winrm username is empty")
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("winrm password is empty")
	}
	params, err := winRMClientParams()
	if err != nil {
		return nil, err
	}
	client, err := winrm.NewClientWithParameters(buildWinRMEndpoint(cfg), cfg.Username, cfg.Password, params)
	if err != nil {
		return nil, fmt.Errorf("winrm client: %w", err)
	}
	return client, nil
}

func (s *winrmSession) Exec(ctx context.Context, command string, stdin []byte) ([]byte, []byte, int, error) {
	var stdout, stderr bytes.Buffer
	var reader *bytes.Reader
	if stdin != nil {
		reader = bytes.NewReader(stdin)
	}
	code, err := s.client.RunWithContextWithInput(ctx, command, &stdout, &stderr, reader)
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), code, err
	}
	return stdout.Bytes(), stderr.Bytes(), code, nil
}

func (s *winrmSession) Upload(context.Context, string, []byte) error {
	return fmt.Errorf("winrm upload is chunked through write-chunk")
}

func (s *winrmSession) Close() error {
	return nil
}
