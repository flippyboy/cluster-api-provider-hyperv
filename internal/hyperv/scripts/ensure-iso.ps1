# script: ensure-iso
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction Stop
$iso = [string]$params.isoPath
if (-not (Test-Path -LiteralPath $iso)) { throw "iso missing: $iso" }
$dvd = Get-VMDvdDrive -VMName $params.vmName | Select-Object -First 1
$needsRestart = $false
if (-not $dvd) {
  Add-VMDvdDrive -VMName $params.vmName -Path $iso | Out-Null
  $needsRestart = ($vm.State -eq 'Running')
} elseif ([string]$dvd.Path -ne $iso) {
  Set-VMDvdDrive -VMName $params.vmName -ControllerNumber $dvd.ControllerNumber -ControllerLocation $dvd.ControllerLocation -Path $iso
  $needsRestart = ($vm.State -eq 'Running')
}
if ($vm.State -eq 'Off') {
  $hdd = Get-VMHardDiskDrive -VMName $params.vmName | Select-Object -First 1
  $dvd = Get-VMDvdDrive -VMName $params.vmName | Select-Object -First 1
  if ($hdd -and $dvd) {
    Set-VMFirmware -VMName $params.vmName -BootOrder $hdd, $dvd
  }
}
@{
  isoPath = $iso
  needsRestart = [bool]$needsRestart
} | ConvertTo-Json -Compress
