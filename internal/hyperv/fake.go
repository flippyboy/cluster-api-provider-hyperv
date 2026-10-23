package hyperv

import (
	"context"
	"encoding/json"
	"sync"
)

// Call is one script invocation recorded by FakeSession.
type Call struct {
	Name   string
	Params json.RawMessage
}

// UploadRecord is one SFTP upload recorded by FakeSession.
type UploadRecord struct {
	Path string
	Data []byte
}

// Responder returns the stdout, stderr, and exit code for one script call.
type Responder func(name string, params json.RawMessage) (stdout, stderr []byte, exitCode int, err error)

// FakeSession records script names and JSON parameters.
// It does not open a network connection.
type FakeSession struct {
	mu       sync.Mutex
	calls    []Call
	uploads  []UploadRecord
	Respond  Responder
	UploadFn func(ctx context.Context, remotePath string, data []byte) error
}

// Calls returns a copy of the recorded script calls.
func (f *FakeSession) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// Uploads returns a copy of the recorded SFTP uploads.
func (f *FakeSession) Uploads() []UploadRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]UploadRecord, len(f.uploads))
	copy(out, f.uploads)
	return out
}

// Exec parses the JSON envelope on stdin and records the script name.
func (f *FakeSession) Exec(ctx context.Context, _ string, stdin []byte) ([]byte, []byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, -1, err
	}
	var env struct {
		Name   string          `json:"name"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(stdin, &env); err != nil {
		return nil, nil, 1, err
	}
	params := append([]byte(nil), env.Params...)
	f.mu.Lock()
	f.calls = append(f.calls, Call{Name: env.Name, Params: params})
	respond := f.Respond
	f.mu.Unlock()
	if respond == nil {
		return []byte(`{}`), nil, 0, nil
	}
	return respond(env.Name, params)
}

// Upload records an SFTP upload.
func (f *FakeSession) Upload(ctx context.Context, remotePath string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.UploadFn != nil {
		return f.UploadFn(ctx, remotePath, data)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, UploadRecord{
		Path: remotePath,
		Data: append([]byte(nil), data...),
	})
	return nil
}

// Close implements Session.
func (f *FakeSession) Close() error {
	return nil
}
