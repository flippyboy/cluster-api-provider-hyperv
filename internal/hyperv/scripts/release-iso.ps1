# script: release-iso
Import-Module Hyper-V -ErrorAction Stop
$iso = [string]$params.isoPath
$released = $false
foreach ($dvd in @(Get-VMDvdDrive -VMName $params.vmName)) {
  if ([string]$dvd.Path -eq $iso) {
    # Same as taliesins/hyperv UpdateVmDvdDrive with an empty path: eject the
    # media so the file can be replaced while the VM is running.
    Set-VMDvdDrive -VMName $params.vmName -ControllerNumber $dvd.ControllerNumber -ControllerLocation $dvd.ControllerLocation -Path $null
    $released = $true
  }
}
@{
  released = [bool]$released
} | ConvertTo-Json -Compress
