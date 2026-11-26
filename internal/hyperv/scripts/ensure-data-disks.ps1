# script: ensure-data-disks
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction Stop
$dir = [string]$vm.Path
if ([string]::IsNullOrWhiteSpace($dir)) { throw "VM $($params.vmName) has no path" }
$items = @()
if ($null -ne $params.disks) {
  $items = @($params.disks)
}
$parts = @()
foreach ($disk in $items) {
  if ($null -eq $disk) { continue }
  $name = [string]$disk.name
  if ($name -cnotmatch '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$') {
    throw "disk name '$name' is invalid"
  }
  $path = Join-Path $dir ("data-" + $name + ".vhdx")
  $sizeGiB = [int64]$disk.sizeGiB
  if (-not (Test-Path -LiteralPath $path)) {
    New-VHD -Path $path -SizeBytes ([int64]$sizeGiB * 1GB) -Dynamic | Out-Null
  }
  $attached = @(Get-VMHardDiskDrive -VMName $params.vmName | Where-Object { $_.Path -eq $path })
  if ($attached.Count -eq 0) {
    Add-VMHardDiskDrive -VMName $params.vmName -Path $path | Out-Null
  }
  $vhd = Get-VHD -Path $path
  $want = $sizeGiB * 1GB
  if ($vhd.Size -lt $want) {
    Resize-VHD -Path $path -SizeBytes $want
  }
  $parts += ConvertTo-Json -Compress -InputObject ([ordered]@{
    name = $name
    diskPath = $path
    sizeGiB = [int]$sizeGiB
  })
}
# One disk must stay a JSON array. ConvertTo-Json unwraps a single element.
'{"disks":[' + ($parts -join ',') + ']}'
