// Package hyperv talks to one Hyper-V host over SSH or WinRM.
// It does not import the Kubernetes API types.
package hyperv

import "context"

// Session is one connection to a Hyper-V host.
// Exec runs one command. stdin is closed after it is copied.
// Upload writes a file. SSH uses SFTP. WinRM uploads are done by Client
// through the write-chunk script, so a WinRM Session rejects Upload.
type Session interface {
	Exec(ctx context.Context, command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error)
	Upload(ctx context.Context, remotePath string, data []byte) error
	Close() error
}
