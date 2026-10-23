# script: write-chunk
$dir = Split-Path -Parent ([string]$params.path)
if (-not [string]::IsNullOrWhiteSpace($dir)) {
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
$bytes = [Convert]::FromBase64String([string]$params.data)
if ([int64]$params.offset -eq 0) {
  [System.IO.File]::WriteAllBytes([string]$params.path, $bytes)
} else {
  $fs = [System.IO.File]::Open([string]$params.path, 'Append', 'Write')
  try {
    $fs.Write($bytes, 0, $bytes.Length)
  } finally {
    $fs.Close()
  }
}
@{
  written = $bytes.Length
  offset = [int64]$params.offset
} | ConvertTo-Json -Compress
