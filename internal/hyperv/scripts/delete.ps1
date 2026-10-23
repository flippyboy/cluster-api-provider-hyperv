# script: delete
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction SilentlyContinue
$dir = $null
if ($vm) {
  $dir = [string]$vm.Path
  if (-not [string]::IsNullOrWhiteSpace($params.imagePath)) {
    $imageParent = (Split-Path -Parent ([string]$params.imagePath)).TrimEnd('\')
    if (-not [string]::IsNullOrWhiteSpace($dir) -and $dir.TrimEnd('\') -eq $imageParent) {
      throw "refusing to delete the golden image directory"
    }
  }
  if ($vm.State -ne 'Off') {
    Stop-VM -Name $params.vmName -TurnOff -Force
  }
  Remove-VM -Name $params.vmName -Force
}
if (-not [string]::IsNullOrWhiteSpace($dir) -and (Test-Path -LiteralPath $dir)) {
  Remove-Item -LiteralPath $dir -Recurse -Force
}
@{ deleted = $true } | ConvertTo-Json -Compress
