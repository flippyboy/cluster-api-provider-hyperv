# script: ensure-disk
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction Stop
$dir = [string]$vm.Path
if ([string]::IsNullOrWhiteSpace($dir)) { throw "VM $($params.vmName) has no path" }
$disk = Join-Path $dir 'disk.vhdx'
$imageParent = Split-Path -Parent ([string]$params.imagePath)
if ($dir.TrimEnd('\') -eq $imageParent.TrimEnd('\')) {
  throw "refusing to place the VM disk in the golden image directory"
}
if (-not (Test-Path -LiteralPath $disk)) {
  if ([string]$params.cloneMode -eq 'Copy') {
    Copy-Item -LiteralPath $params.imagePath -Destination $disk
  } else {
    New-VHD -Path $disk -ParentPath $params.imagePath -Differencing | Out-Null
  }
}
# An automatic checkpoint attaches the avhdx child, not disk.vhdx.
# Adding disk.vhdx again fails because the parent is already open.
$attached = $false
foreach ($drive in @(Get-VMHardDiskDrive -VMName $params.vmName)) {
  $path = [string]$drive.Path
  $guard = 0
  while ($path -and $guard -lt 8) {
    if ($path -eq $disk) { $attached = $true; break }
    $vhd = Get-VHD -Path $path -ErrorAction SilentlyContinue
    if (-not $vhd) { break }
    $path = [string]$vhd.ParentPath
    $guard++
  }
  if ($attached) { break }
}
if (-not $attached) {
  Add-VMHardDiskDrive -VMName $params.vmName -Path $disk
}
if ($null -ne $params.sizeGiB -and [int64]$params.sizeGiB -gt 0) {
  $vhd = Get-VHD -Path $disk
  $want = [int64]$params.sizeGiB * 1GB
  if ($vhd.Size -lt $want) {
    Resize-VHD -Path $disk -SizeBytes $want
  }
}
@{
  diskPath = $disk
  cloneMode = [string]$params.cloneMode
} | ConvertTo-Json -Compress
