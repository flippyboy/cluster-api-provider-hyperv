# script: probe
Import-Module Hyper-V -ErrorAction Stop
$switch = Get-VMSwitch -Name $params.virtualSwitch -ErrorAction SilentlyContinue
$imageFound = $false
$imageIsVhdx = $false
if (-not [string]::IsNullOrWhiteSpace($params.imagePath)) {
  $imageFound = [bool](Test-Path -LiteralPath $params.imagePath)
  $ext = [System.IO.Path]::GetExtension([string]$params.imagePath)
  $imageIsVhdx = ($ext -eq '.vhdx')
}
$hostInfo = Get-VMHost
@{
  hypervModule = $true
  switchFound = [bool]$switch
  imageFound = $imageFound
  imageIsVhdx = $imageIsVhdx
  virtualMachinePath = [string]$hostInfo.VirtualMachinePath
} | ConvertTo-Json -Compress
