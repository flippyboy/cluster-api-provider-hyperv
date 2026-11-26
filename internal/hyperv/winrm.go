package hyperv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
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
	if len(stdin) == 0 {
		return s.run(ctx, command)
	}
	// The WinRM cmd shell does not signal stdin EOF. taliesins/hyperv uploads
	// the script and runs it with no stdin. Do the same.
	return s.runUploadedScript(ctx, scriptFile(stdinPayload(stdin)))
}

func (s *winrmSession) run(ctx context.Context, command string) ([]byte, []byte, int, error) {
	var stdout, stderr bytes.Buffer
	code, err := s.client.RunWithContextWithInput(ctx, command, &stdout, &stderr, nil)
	return stdout.Bytes(), stderr.Bytes(), code, err
}

// winRMChunkChars keeps each cmd.exe command under the 8191 character limit.
const winRMChunkChars = 7000

func (s *winrmSession) runUploadedScript(ctx context.Context, script string) ([]byte, []byte, int, error) {
	tempOut, tempErr, code, err := s.run(ctx, `powershell.exe -NoProfile -NonInteractive -Command "Write-Output $env:TEMP"`)
	if err != nil {
		return tempOut, tempErr, code, fmt.Errorf("resolve temp: %w", err)
	}
	if code != 0 {
		return tempOut, tempErr, code, fmt.Errorf("resolve temp exited %d: %s", code, clip(append(tempErr, tempOut...)))
	}
	temp := lastNonEmptyLine(tempOut)
	if !validWinPath(temp) {
		return tempOut, tempErr, code, fmt.Errorf("resolve temp returned %q", temp)
	}

	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, nil, 0, err
	}
	tag := hex.EncodeToString(id[:])
	b64Path := temp + `\caphv-` + tag + `.b64`
	ps1Path := temp + `\caphv-` + tag + `.ps1`
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _, _, _ = s.run(cleanCtx, `powershell.exe -NoProfile -NonInteractive -Command "Remove-Item -LiteralPath @('`+psQuote(b64Path)+`','`+psQuote(ps1Path)+`') -Force -ErrorAction SilentlyContinue"`)
	}()

	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	for i := 0; i < len(encoded); i += winRMChunkChars {
		j := i + winRMChunkChars
		if j > len(encoded) {
			j = len(encoded)
		}
		cmd := `powershell.exe -NoProfile -NonInteractive -Command "[IO.File]::AppendAllText('` + psQuote(b64Path) + `','` + encoded[i:j] + `'+[char]10)"`
		if len(cmd) > 8000 {
			return nil, nil, 0, fmt.Errorf("winrm append command is %d characters", len(cmd))
		}
		stdout, stderr, code, err := s.run(ctx, cmd)
		if err != nil {
			return stdout, stderr, code, fmt.Errorf("upload script: %w", err)
		}
		if code != 0 {
			return stdout, stderr, code, fmt.Errorf("upload script exited %d: %s", code, clip(append(stderr, stdout...)))
		}
	}

	exec := `powershell.exe -NoProfile -NonInteractive -Command "$ErrorActionPreference='Stop'; $in='` + psQuote(b64Path) + `'; $out='` + psQuote(ps1Path) + `'; $r=[IO.File]::OpenText($in); $w=[IO.File]::Create($out); try { while($null -ne ($line=$r.ReadLine())) { if($line -eq ''){continue}; $b=[Convert]::FromBase64String($line); $w.Write($b,0,$b.Length) } } finally { $w.Dispose(); $r.Dispose() }; & $out"`
	return s.run(ctx, exec)
}

func psQuote(path string) string {
	return strings.ReplaceAll(path, `'`, `''`)
}

func lastNonEmptyLine(b []byte) string {
	lines := strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' || r == '\r' })
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func validWinPath(path string) bool {
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return false
	}
	return !strings.ContainsAny(path, "'\n\r")
}

func (s *winrmSession) Upload(context.Context, string, []byte) error {
	return fmt.Errorf("winrm upload is chunked through write-chunk")
}

func (s *winrmSession) Close() error {
	return nil
}
