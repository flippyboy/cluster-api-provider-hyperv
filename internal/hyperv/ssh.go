package hyperv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const sshDialTimeout = 30 * time.Second

type sshSession struct {
	client *ssh.Client
}

func dialSSH(ctx context.Context, cfg HostConfig) (*sshSession, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("ssh host address is empty")
	}
	if cfg.Username == "" {
		return nil, fmt.Errorf("ssh username is empty")
	}
	signer, err := signerFromKey(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	hostKey, err := hostKeyCallback(cfg.KnownHosts, cfg.InsecureSkipHostKey)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Address, fmt.Sprintf("%d", cfg.DialPort()))
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKey,
		Timeout:         sshDialTimeout,
	}
	dialer := net.Dialer{Timeout: sshDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	close(handshakeDone)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", addr, err)
	}
	return &sshSession{client: ssh.NewClient(cc, chans, reqs)}, nil
}

func signerFromKey(key []byte) (ssh.Signer, error) {
	if len(bytes.TrimSpace(key)) == 0 {
		return nil, fmt.Errorf("ssh private key is empty")
	}
	raw, err := ssh.ParseRawPrivateKey(key)
	if err != nil {
		var passphrase *ssh.PassphraseMissingError
		if errors.As(err, &passphrase) {
			return nil, fmt.Errorf("encrypted SSH private keys are not supported")
		}
		return nil, fmt.Errorf("parse ssh private key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, fmt.Errorf("ssh signer: %w", err)
	}
	return signer, nil
}

func hostKeyCallback(known []byte, skip bool) (ssh.HostKeyCallback, error) {
	if skip {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if len(bytes.TrimSpace(known)) == 0 {
		return nil, fmt.Errorf("ssh-known-hosts is empty")
	}
	f, err := os.CreateTemp("", "hyperv-known-hosts-*")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(known); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	// knownhosts.New reads the file into memory.
	cb, err := knownhosts.New(name)
	if err != nil {
		return nil, fmt.Errorf("parse ssh-known-hosts: %w", err)
	}
	return cb, nil
}

func (s *sshSession) Exec(ctx context.Context, command string, stdin []byte) ([]byte, []byte, int, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return nil, nil, -1, err
	}
	defer sess.Close()
	if len(stdin) > 0 {
		sess.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	runDone := make(chan error, 1)
	go func() {
		runDone <- sess.Run(command)
	}()
	select {
	case <-ctx.Done():
		_ = sess.Close()
		return stdout.Bytes(), stderr.Bytes(), -1, ctx.Err()
	case err := <-runDone:
		if err == nil {
			return stdout.Bytes(), stderr.Bytes(), 0, nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), stderr.Bytes(), exitErr.ExitStatus(), nil
		}
		return stdout.Bytes(), stderr.Bytes(), -1, err
	}
}

// sftpPath uses forward slashes. Win32-OpenSSH accepts C:/path.
func sftpPath(remotePath string) string {
	return strings.ReplaceAll(remotePath, `\`, "/")
}

func (s *sshSession) Upload(ctx context.Context, remotePath string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sc, err := sftp.NewClient(s.client)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close()
	path := sftpPath(remotePath)
	if i := strings.LastIndex(path, "/"); i > 0 {
		if err := sc.MkdirAll(path[:i]); err != nil {
			return fmt.Errorf("sftp mkdir %s: %w", path[:i], err)
		}
	}
	f, err := sc.Create(path)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("sftp write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("sftp close %s: %w", path, err)
	}
	return nil
}

func (s *sshSession) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
