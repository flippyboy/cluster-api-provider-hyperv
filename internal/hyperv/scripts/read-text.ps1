# script: read-text
$path = [string]$params.path
$exists = Test-Path -LiteralPath $path
$text = ''
if ($exists) {
  try {
    $text = ([System.IO.File]::ReadAllText($path)).Trim()
  } catch {
    $text = ''
  }
}
@{
  exists = [bool]$exists
  text = [string]$text
} | ConvertTo-Json -Compress
