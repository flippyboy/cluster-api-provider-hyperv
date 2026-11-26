package hyperv

import (
	"encoding/base64"
	"encoding/binary"
	"unicode/utf16"
)

// bootstrapScript is sent as powershell.exe -EncodedCommand.
// The script body and its parameters travel as one length-prefixed JSON
// object on stdin, so user strings are never PowerShell tokens.
// WinRM delivers the bytes on the standard input stream and does not signal
// end-of-file, so [Console]::In.ReadToEnd blocks until the command timeout.
// The first four bytes are a little-endian length. The reader stops there.
const bootstrapScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$utf8 = New-Object System.Text.UTF8Encoding $false
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
try {
  $stdin = [Console]::OpenStandardInput()
  $lenBuf = New-Object byte[] 4
  $got = 0
  while ($got -lt 4) {
    $n = $stdin.Read($lenBuf, $got, 4 - $got)
    if ($n -le 0) { throw 'stdin closed before length' }
    $got += $n
  }
  $length = [int][BitConverter]::ToUInt32($lenBuf, 0)
  if ($length -le 0 -or $length -gt 16777216) { throw "stdin length $length is too large" }
  $data = New-Object byte[] $length
  $got = 0
  while ($got -lt $length) {
    $n = $stdin.Read($data, $got, $length - $got)
    if ($n -le 0) { throw 'stdin closed before payload' }
    $got += $n
  }
  $raw = $utf8.GetString($data)
  if ([string]::IsNullOrWhiteSpace($raw)) { throw 'empty stdin' }
  $envelope = $raw | ConvertFrom-Json
  $params = $envelope.params
  $sb = [scriptblock]::Create([string]$envelope.script)
  & $sb
} catch {
  @{ error = [string]$_.Exception.Message } | ConvertTo-Json -Compress
  exit 1
}
`

// frameStdin prefixes payload with its little-endian length.
// SSH closes stdin, so the remote reader can stop on that prefix.
func frameStdin(payload []byte) []byte {
	buf := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(payload)))
	copy(buf[4:], payload)
	return buf
}

// scriptFile is the PowerShell file WinRM executes.
// WinRM delivers stdin bytes and does not signal end-of-file, so the
// envelope is embedded here and the file is uploaded with command-line
// writes. The same shape is what taliesins/hyperv runs: a script on disk,
// then powershell with no stdin.
func scriptFile(payload []byte) string {
	return `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$utf8 = New-Object System.Text.UTF8Encoding $false
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
try {
  $raw = $utf8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString(payload) + `'))
  $envelope = $raw | ConvertFrom-Json
  $params = $envelope.params
  $sb = [scriptblock]::Create([string]$envelope.script)
  & $sb
} catch {
  @{ error = [string]$_.Exception.Message } | ConvertTo-Json -Compress
  exit 1
}
`
}

func encodeUTF16LEBase64(s string) string {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[i*2:], u)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func powershellCommand() string {
	return "powershell.exe -NoProfile -NonInteractive -EncodedCommand " + encodeUTF16LEBase64(bootstrapScript)
}
