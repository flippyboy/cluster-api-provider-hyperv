package hyperv

import (
	"encoding/base64"
	"encoding/binary"
	"unicode/utf16"
)

// bootstrapScript is sent as powershell.exe -EncodedCommand.
// The script body and its parameters travel as one JSON object on stdin,
// so user strings are never PowerShell tokens.
// -NonInteractive stays. Drop it if a host shows that it blocks
// [Console]::In.ReadToEnd.
const bootstrapScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$utf8 = New-Object System.Text.UTF8Encoding $false
[Console]::InputEncoding = $utf8
[Console]::OutputEncoding = $utf8
$OutputEncoding = $utf8
try {
  $raw = [Console]::In.ReadToEnd()
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
